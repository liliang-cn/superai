package backend

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/liliang-cn/agent-go/v3/pkg/agent"
)

// decider judges whether a call is destructive from its text.
//
// It may only ever add an ask. The model behind it answers in milliseconds and
// is wrong with high confidence on negated text — "rm -rf /tmp/x, do not touch
// anything else" is exactly the shape it misreads — so it is not allowed to
// wave anything through. A false positive costs one prompt; a false negative
// would be a command running unasked.
type decider interface {
	destructive(ctx context.Context, text string) (bool, error)
}

// policyWith returns the approval policy, optionally widened by a decider.
//
// With no decider this is the name-matching policy unchanged.
func policyWith(d decider) agent.PermissionPolicy {
	return func(req agent.PermissionRequest) bool {
		if toolNeedsApproval(req) {
			return true
		}
		// Read-only calls change nothing, so there is nothing to approve and
		// no reason to spend a decision on one.
		if d == nil || req.ReadOnly {
			return false
		}
		text := describeCall(req)
		if text == "" {
			return false
		}
		ctx, cancel := context.WithTimeout(context.Background(), deciderBudget)
		defer cancel()
		yes, err := d.destructive(ctx, text)
		if err != nil {
			// Losing the service leaves today's behaviour. Flagging everything
			// on an outage would train people to click through the prompt.
			return false
		}
		return yes
	}
}

// deciderBudget caps how long a permission check may wait. The gate sits in
// front of every tool call, and the model answers in tens of milliseconds.
const deciderBudget = 2 * time.Second

// describeCall renders a call as the sentence the model judges.
//
// Arguments are included because the name rules already cover names: what they
// cannot see is a destructive command carried by a tool called something else.
func describeCall(req agent.PermissionRequest) string {
	var b strings.Builder
	b.WriteString(req.ToolName)
	if cmd := approvalCommand(req.ToolName, req.ToolArgs); cmd != "" {
		b.WriteString(": ")
		b.WriteString(cmd)
		return truncate(b.String(), maxDecisionText)
	}
	keys := make([]string, 0, len(req.ToolArgs))
	for k := range req.ToolArgs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v, ok := req.ToolArgs[k].(string)
		if !ok || strings.TrimSpace(v) == "" {
			continue
		}
		b.WriteString(" ")
		b.WriteString(k)
		b.WriteString("=")
		b.WriteString(v)
	}
	return truncate(b.String(), maxDecisionText)
}

// maxDecisionText bounds what is sent. The checkpoints take 512 tokens; a file
// body pasted into an argument would otherwise be truncated by the tokenizer
// at a point nobody chose.
const maxDecisionText = 1500

// layaDecider asks a laya-serve instance.
type layaDecider struct {
	url       string
	threshold float64
	client    *http.Client
}

// newLayaDecider builds a decider against a laya-serve base URL. threshold is
// the probability at or above which a call is treated as destructive.
func newLayaDecider(baseURL string, threshold float64, timeout time.Duration) decider {
	if strings.TrimSpace(baseURL) == "" {
		return nil
	}
	return &layaDecider{
		url:       strings.TrimRight(baseURL, "/") + "/decide",
		threshold: threshold,
		client:    &http.Client{Timeout: timeout},
	}
}

type layaRequest struct {
	Text      string                  `json:"text"`
	Questions map[string]layaQuestion `json:"questions"`
	Model     string                  `json:"model,omitempty"`
}

type layaQuestion struct {
	Type         string   `json:"type"`
	Instructions string   `json:"instructions"`
	Criteria     []string `json:"criteria,omitempty"`
}

type layaResponse struct {
	Answers map[string]struct {
		Type string   `json:"type"`
		Noul *float64 `json:"noul"`
	} `json:"answers"`
}

// destructiveQuestion and DefaultDeciderThreshold were chosen by measurement,
// not by taste. Over fifteen labelled calls this wording missed no dangerous
// one at 0.15 and cost two extra prompts; asking whether the action is
// "impossible to undo" missed five, and the multilingual checkpoint missed
// seven. Fifteen cases is a small set — re-measure before trusting it wider.
//
// The threshold is low because this checkpoint's probabilities sit low: laya
// itself warns that it ships temperatures outside the calibrated range. A 0.5
// threshold reads as "confident" and lets a write to authorized_keys through.
const destructiveQuestion = "Is this a dangerous operation that a careful person " +
	"would want to confirm first?"

// DefaultDeciderThreshold is the probability at or above which a call is
// treated as needing approval.
const DefaultDeciderThreshold = 0.15

func (d *layaDecider) destructive(ctx context.Context, text string) (bool, error) {
	payload, err := json.Marshal(layaRequest{
		Text: text,
		Questions: map[string]layaQuestion{
			"destructive": {Type: "noul", Instructions: destructiveQuestion},
		},
	})
	if err != nil {
		return false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.url, bytes.NewReader(payload))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := d.client.Do(req)
	if err != nil {
		return false, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("decide: %s", resp.Status)
	}

	var out layaResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return false, err
	}
	a, ok := out.Answers["destructive"]
	if !ok || a.Noul == nil {
		return false, fmt.Errorf("decide: no answer")
	}
	return *a.Noul >= d.threshold, nil
}
