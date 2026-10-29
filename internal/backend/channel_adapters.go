package backend

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// One adapter per channel type: the envelope, the signature and the idea of
// failure each service has. Formats are the services' own documented ones:
//
//   - Feishu custom bot: POST {"msg_type":"text","content":{"text":…}};
//     signed with timestamp (seconds) and sign = base64(HMAC-SHA256(key =
//     timestamp+"\n"+secret, message = "")) in the body. Answers HTTP 200
//     with {"code":0} on success and a non-zero code on failure. Body ≤ 20 KB.
//   - WeCom group bot: POST /cgi-bin/webhook/send?key=…
//     {"msgtype":"text","text":{"content":…}}, content ≤ 2048 bytes. Answers
//     {"errcode":0,"errmsg":"ok"}.
//   - DingTalk robot: POST /robot/send?access_token=…
//     {"msgtype":"markdown","markdown":{"title":…,"text":…}}; signed with
//     &timestamp=<ms>&sign=urlencode(base64(HMAC-SHA256(key = secret,
//     message = timestamp+"\n"+secret))). Answers {"errcode":0}.
//   - Slack incoming webhook: POST {"text":…} (mrkdwn). Answers 200 "ok",
//     and 4xx with a reason (invalid_payload, no_text …) on failure.
//   - Bark: POST <server>/push {"device_key","title","body","group","level"}
//     (bark-server API v2). Answers {"code":200}.
//   - Telegram: Bot API sendMessage to each allowed chat.
//   - Generic webhook: WebhookPayload as JSON, Authorization: Bearer <secret>.
//
// Every limit below is the service's; the text is cut to fit rather than
// refused, because a reminder that arrives shortened beats one that does not.

// telegramAPIBase is the Bot API host; tests point it at a local server.
var telegramAPIBase = "https://api.telegram.org"

func deliverChannel(ctx context.Context, client *http.Client, c Channel, p WebhookPayload) error {
	switch c.Type {
	case ChannelWebhook:
		return sendWebhook(ctx, client, c, p)
	case ChannelFeishu:
		return sendFeishu(ctx, client, c, p, time.Now())
	case ChannelWeCom:
		return sendWeCom(ctx, client, c, p)
	case ChannelDingTalk:
		return sendDingTalk(ctx, client, c, p, time.Now())
	case ChannelSlack:
		return sendSlack(ctx, client, c, p)
	case ChannelBark:
		return sendBark(ctx, client, c, p)
	case ChannelTelegram:
		return sendTelegramChannel(ctx, client, c, p)
	}
	return fmt.Errorf("unknown channel type %q", c.Type)
}

// plainText is the message as a person reads it in a chat: the title on its
// own line when it says more than the app's name, then the message.
func plainText(p WebhookPayload) string {
	msg := strings.TrimSpace(p.Message)
	title := strings.TrimSpace(p.Title)
	if title == "" || title == "SuperLeo" {
		return msg
	}
	return title + "\n" + msg
}

func sendWebhook(ctx context.Context, client *http.Client, c Channel, p WebhookPayload) error {
	// Exactly application/json, as it always was: a receiver may compare it.
	headers := map[string]string{"Content-Type": "application/json"}
	if c.Secret != "" {
		// Bearer rather than a signature: the receiver is usually a few lines
		// of script, and a shared secret it can compare is the thing it will
		// actually check. A signature nobody verifies is worse than a token.
		headers["Authorization"] = "Bearer " + c.Secret
	}
	_, err := postJSON(ctx, client, c.URL, p, headers)
	return err
}

// FeishuSign is the custom bot's signature for a timestamp in seconds.
func FeishuSign(secret string, timestamp int64) string {
	mac := hmac.New(sha256.New, []byte(strconv.FormatInt(timestamp, 10)+"\n"+secret))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func sendFeishu(ctx context.Context, client *http.Client, c Channel, p WebhookPayload, now time.Time) error {
	body := map[string]any{
		"msg_type": "text",
		// 20 KB is the whole body; the text leaves room for the envelope.
		"content": map[string]string{"text": truncateBytes(plainText(p), 18000)},
	}
	if c.Secret != "" {
		ts := now.Unix()
		body["timestamp"] = strconv.FormatInt(ts, 10)
		body["sign"] = FeishuSign(c.Secret, ts)
	}
	raw, err := postJSON(ctx, client, c.URL, body, nil)
	if err != nil {
		return err
	}
	var out struct {
		Code       *int   `json:"code"`
		Msg        string `json:"msg"`
		StatusCode *int   `json:"StatusCode"`
	}
	if json.Unmarshal(raw, &out) != nil {
		return fmt.Errorf("feishu answered %q", snippet(raw))
	}
	if out.Code != nil && *out.Code != 0 {
		return fmt.Errorf("feishu: %d %s", *out.Code, out.Msg)
	}
	if out.Code == nil && out.StatusCode != nil && *out.StatusCode != 0 {
		return fmt.Errorf("feishu: %d", *out.StatusCode)
	}
	return nil
}

// weComURL accepts the full webhook address or its bare key.
func weComURL(v string) string {
	if strings.HasPrefix(v, "http://") || strings.HasPrefix(v, "https://") {
		return v
	}
	return "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=" + url.QueryEscape(v)
}

func sendWeCom(ctx context.Context, client *http.Client, c Channel, p WebhookPayload) error {
	// Text rather than markdown: WeCom's markdown cannot be opened from
	// WeChat, where most people read a group bot.
	body := map[string]any{
		"msgtype": "text",
		"text":    map[string]string{"content": truncateBytes(plainText(p), 2048)},
	}
	raw, err := postJSON(ctx, client, weComURL(c.URL), body, nil)
	if err != nil {
		return err
	}
	return errcodeOK("wecom", raw)
}

// DingTalkSign is the robot's signature for a timestamp in milliseconds,
// before URL encoding.
func DingTalkSign(secret string, timestampMs int64) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strconv.FormatInt(timestampMs, 10) + "\n" + secret))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// dingTalkURL accepts the full webhook address or its bare access_token, and
// adds the signature when there is a secret.
func dingTalkURL(v, secret string, now time.Time) (string, error) {
	if !strings.HasPrefix(v, "http://") && !strings.HasPrefix(v, "https://") {
		v = "https://oapi.dingtalk.com/robot/send?access_token=" + url.QueryEscape(v)
	}
	if secret == "" {
		return v, nil
	}
	u, err := url.Parse(v)
	if err != nil {
		return "", err
	}
	ts := now.UnixMilli()
	q := u.Query()
	q.Set("timestamp", strconv.FormatInt(ts, 10))
	q.Set("sign", DingTalkSign(secret, ts))
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func sendDingTalk(ctx context.Context, client *http.Client, c Channel, p WebhookPayload, now time.Time) error {
	endpoint, err := dingTalkURL(c.URL, c.Secret, now)
	if err != nil {
		return err
	}
	title := strings.TrimSpace(p.Title)
	if title == "" {
		title = "SuperLeo"
	}
	// Markdown: the answers are written in it, and DingTalk renders it. The
	// title is what the conversation list and the banner show.
	text := strings.TrimSpace(p.Message)
	if title != "SuperLeo" {
		text = "#### " + title + "\n\n" + text
	}
	body := map[string]any{
		"msgtype":  "markdown",
		"markdown": map[string]string{"title": truncateRunes(title, 60), "text": truncateBytes(text, 18000)},
	}
	raw, err := postJSON(ctx, client, endpoint, body, nil)
	if err != nil {
		return err
	}
	return errcodeOK("dingtalk", raw)
}

func sendSlack(ctx context.Context, client *http.Client, c Channel, p WebhookPayload) error {
	text := strings.TrimSpace(p.Message)
	if t := strings.TrimSpace(p.Title); t != "" && t != "SuperLeo" {
		text = "*" + t + "*\n" + text
	}
	_, err := postJSON(ctx, client, c.URL, map[string]string{"text": text}, nil)
	return err
}

func sendBark(ctx context.Context, client *http.Client, c Channel, p WebhookPayload) error {
	server := c.Server
	if server == "" {
		server = "https://api.day.app"
	}
	level := "active"
	switch eventOf(p) {
	case ChannelEventNeedsYou, ChannelEventFailed:
		// Breaks through Focus: something is waiting on the person, or broke.
		level = "timeSensitive"
	}
	body := map[string]string{
		"device_key": c.DeviceKey,
		"title":      strings.TrimSpace(p.Title),
		// A push is four kilobytes all told; a phone banner shows a few lines.
		"body":  truncateRunes(strings.TrimSpace(p.Message), 1000),
		"group": "SuperLeo",
		"level": level,
	}
	raw, err := postJSON(ctx, client, server+"/push", body, nil)
	if err != nil {
		return err
	}
	var out struct {
		Code    *int   `json:"code"`
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &out) == nil && out.Code != nil && *out.Code != http.StatusOK {
		return fmt.Errorf("bark: %d %s", *out.Code, out.Message)
	}
	return nil
}

func sendTelegramChannel(ctx context.Context, client *http.Client, c Channel, p WebhookPayload) error {
	var errs []error
	for _, chat := range c.AllowedChats {
		for _, part := range splitForTelegram(plainText(p), telegramMessageLimit) {
			q := url.Values{}
			q.Set("chat_id", strconv.FormatInt(chat, 10))
			q.Set("text", part)
			endpoint := fmt.Sprintf("%s/bot%s/sendMessage", strings.TrimRight(telegramAPIBase, "/"), c.BotToken)
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(q.Encode()))
			if err != nil {
				return err
			}
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			resp, err := client.Do(req)
			if err != nil {
				// The URL carries the token; the error text would repeat it.
				var ue *url.Error
				if errors.As(err, &ue) {
					err = ue.Err
				}
				errs = append(errs, fmt.Errorf("chat %d: %v", chat, err))
				break
			}
			raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				errs = append(errs, fmt.Errorf("chat %d: %s %s", chat, resp.Status, snippet(raw)))
				break
			}
		}
	}
	return errors.Join(errs...)
}

// postJSON posts a JSON body and returns the answer's body; any status
// outside 2xx is an error carrying the start of what the service said.
func postJSON(ctx context.Context, client *http.Client, endpoint string, body any, headers map[string]string) ([]byte, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, errors.New("not a URL")
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		// Without the URL: for every bot type it carries the key.
		var ue *url.Error
		if errors.As(err, &ue) {
			return nil, fmt.Errorf("%s %s: %v", strings.ToUpper(ue.Op), redactURL(endpoint), ue.Err)
		}
		return nil, err
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return out, fmt.Errorf("%s %s", resp.Status, snippet(out))
	}
	return out, nil
}

func errcodeOK(service string, raw []byte) error {
	var out struct {
		Errcode *int   `json:"errcode"`
		Errmsg  string `json:"errmsg"`
	}
	if json.Unmarshal(raw, &out) != nil || out.Errcode == nil {
		return fmt.Errorf("%s answered %q", service, snippet(raw))
	}
	if *out.Errcode != 0 {
		return fmt.Errorf("%s: %d %s", service, *out.Errcode, out.Errmsg)
	}
	return nil
}

func snippet(b []byte) string {
	return truncateRunes(strings.TrimSpace(string(b)), 200)
}

// redactURL keeps the host and hides what identifies the bot.
func redactURL(v string) string {
	u, err := url.Parse(v)
	if err != nil || u.Host == "" {
		return "the URL"
	}
	return u.Scheme + "://" + u.Host + "/…"
}
