package backend

// Market data for the home screen's widgets: a price, the day's change and a
// month of daily closes for a sparkline, for the person's holdings and for any
// symbol they want to watch.
//
// The source is Yahoo Finance's chart API (query1.finance.yahoo.com/v8/finance/
// chart/<symbol>), the one the yfinance library reads: no key, every exchange,
// funds, crypto, futures and FX in one shape. It is unofficial — it can rate
// limit or change — so everything here is cached and a failed fetch falls back
// to the last good answer:
//
//   - the price and the day's change: range=5d, kept 60 s;
//   - the closes: range=3mo, kept 6 h (they change once a day), with the 5d
//     bars laid over the end so a new day shows up without waiting.
//
// Names the person uses are mapped to Yahoo's: BTC → BTC-USD, 0700 → 0700.HK,
// 600519 → 600519.SS, and 黄金 → gold in CNY per gram, worked out from the
// COMEX future (GC=F, USD per troy ounce) and the dollar's CNY rate (CNY=X).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// ErrUnknownSymbol is a symbol the source has no data for.
var ErrUnknownSymbol = errors.New("unknown symbol")

const (
	quoteLiveTTL    = 60 * time.Second
	quoteHistoryTTL = 6 * time.Hour
	// quoteUnknownTTL keeps a "no such symbol" answer, so a typo is not asked
	// about again on every keystroke's refresh.
	quoteUnknownTTL = 10 * time.Minute
	// QuoteMaxSymbols bounds one Quotes call.
	QuoteMaxSymbols = 20
	// quoteCloses is how many daily closes a quote carries.
	quoteCloses       = 30
	gramsPerTroyOunce = 31.1034768
	// goldCNYPerGram is the derived symbol for gold in yuan per gram.
	goldCNYPerGram = "GOLD-CNY"
)

// Quote is one symbol's market data, as the Quotes RPC returns it.
type Quote struct {
	// Symbol is what was asked for; Source is what the source was asked.
	Symbol        string  `json:"symbol"`
	Source        string  `json:"source"`
	Name          string  `json:"name,omitempty"`
	Price         float64 `json:"price"`
	PreviousClose float64 `json:"previousClose,omitempty"`
	Change        float64 `json:"change"`
	ChangePercent float64 `json:"changePercent"`
	Currency      string  `json:"currency,omitempty"`
	// Unit is what one price is for when it is not one share or coin: "g".
	Unit string     `json:"unit,omitempty"`
	Time *time.Time `json:"time,omitempty"`
	// Closes are daily closes, oldest first; the last is the current price.
	Closes []float64 `json:"closes"`
	// Stale is an answer older than the cache wanted, given because the
	// source could not be reached.
	Stale bool `json:"stale,omitempty"`
}

// QuoteService fetches and caches quotes. The zero value is not usable; see
// NewQuoteService.
type QuoteService struct {
	base   string
	client *http.Client
	now    func() time.Time

	mu      sync.Mutex
	live    map[string]liveEntry
	history map[string]historyEntry
}

type liveEntry struct {
	at    time.Time
	chart chartData
	err   error
}

type historyEntry struct {
	at     time.Time
	points []closePoint
}

type closePoint struct {
	day   string // YYYY-MM-DD in the exchange's zone
	close float64
}

// chartData is what one chart response says.
type chartData struct {
	symbol   string
	name     string
	currency string
	price    float64
	at       time.Time
	zone     *time.Location
	points   []closePoint
}

// NewQuoteService talks to base (empty: Yahoo). Requests go through
// SUPERLEO_QUOTE_PROXY when it is set, else the usual proxy environment.
func NewQuoteService(base string) *QuoteService {
	if base == "" {
		base = "https://query1.finance.yahoo.com"
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if p := strings.TrimSpace(os.Getenv("SUPERLEO_QUOTE_PROXY")); p != "" {
		if u, err := url.Parse(p); err == nil {
			tr.Proxy = http.ProxyURL(u)
		}
	}
	return &QuoteService{
		base:    strings.TrimRight(base, "/"),
		client:  &http.Client{Timeout: 10 * time.Second, Transport: tr},
		now:     time.Now,
		live:    map[string]liveEntry{},
		history: map[string]historyEntry{},
	}
}

var (
	quoteCryptoBare = map[string]bool{"BTC": true, "ETH": true, "SOL": true, "BNB": true, "XRP": true, "DOGE": true,
		"ADA": true, "TON": true, "TRX": true, "AVAX": true, "DOT": true, "LTC": true, "LINK": true}
	quoteGoldCNY = map[string]bool{"黄金": true, "金价": true, "GOLD-CNY": true, "XAUCNY": true, "XAU-CNY": true, "AU9999": true}
	quoteGoldUSD = map[string]bool{"XAU": true, "XAUUSD": true, "XAU-USD": true, "GOLD-USD": true}
	quoteCNCode  = regexp.MustCompile(`^\d{6}$`)
	quoteHKCode  = regexp.MustCompile(`^\d{1,5}$`)
)

// NormalizeQuoteSymbol is a symbol as it is stored and shown: trimmed, and
// upper case where it is ASCII.
func NormalizeQuoteSymbol(s string) string {
	s = strings.TrimSpace(s)
	b := []rune(s)
	for i, r := range b {
		if r < 128 {
			b[i] = []rune(strings.ToUpper(string(r)))[0]
		}
	}
	return string(b)
}

// QuoteSourceSymbol maps a symbol as the person writes it to the source's.
func QuoteSourceSymbol(s string) string {
	s = NormalizeQuoteSymbol(s)
	switch {
	case quoteGoldCNY[s]:
		return goldCNYPerGram
	case quoteGoldUSD[s]:
		return "GC=F"
	case quoteCryptoBare[s]:
		return s + "-USD"
	case quoteCNCode.MatchString(s):
		// Shanghai: 6xxxxx shares, 5xxxxx funds, 9xxxxx B shares; the rest
		// is Shenzhen.
		if strings.ContainsAny(s[:1], "569") {
			return s + ".SS"
		}
		return s + ".SZ"
	case quoteHKCode.MatchString(s):
		n := strings.TrimLeft(s, "0")
		if len(n) < 4 {
			n = strings.Repeat("0", 4-len(n)) + n
		}
		return n + ".HK"
	}
	return s
}

// Quotes answers several symbols at once, in the order asked; the ones that
// could not be answered are in the map with why.
func (q *QuoteService) Quotes(ctx context.Context, symbols []string) ([]Quote, map[string]string) {
	seen := map[string]bool{}
	var asked []string
	for _, s := range symbols {
		s = NormalizeQuoteSymbol(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		asked = append(asked, s)
		if len(asked) == QuoteMaxSymbols {
			break
		}
	}
	quotes := make([]*Quote, len(asked))
	errs := make([]error, len(asked))
	var wg sync.WaitGroup
	for i, s := range asked {
		wg.Add(1)
		go func(i int, s string) {
			defer wg.Done()
			r, err := q.Quote(ctx, s)
			quotes[i], errs[i] = r, err
		}(i, s)
	}
	wg.Wait()
	out := []Quote{}
	bad := map[string]string{}
	for i, s := range asked {
		if errs[i] != nil {
			if errors.Is(errs[i], ErrUnknownSymbol) {
				bad[s] = "unknown symbol"
			} else {
				bad[s] = errs[i].Error()
			}
			continue
		}
		out = append(out, *quotes[i])
	}
	return out, bad
}

// Check says whether a symbol has data: ErrUnknownSymbol when the source
// says it has none, another error when it could not be asked.
func (q *QuoteService) Check(ctx context.Context, symbol string) error {
	_, err := q.Quote(ctx, symbol)
	return err
}

// Quote is one symbol.
func (q *QuoteService) Quote(ctx context.Context, symbol string) (*Quote, error) {
	asked := NormalizeQuoteSymbol(symbol)
	src := QuoteSourceSymbol(asked)
	if src == goldCNYPerGram {
		return q.goldCNY(ctx, asked)
	}
	live, stale, err := q.liveChart(ctx, src)
	if err != nil {
		return nil, err
	}
	hist := q.historyPoints(ctx, src)
	r := assembleQuote(asked, src, live, hist)
	r.Stale = stale
	return r, nil
}

// assembleQuote makes a quote from the live chart and the longer history.
func assembleQuote(asked, src string, live chartData, hist []closePoint) *Quote {
	today := live.at.In(live.zone).Format("2006-01-02")
	points := mergeCloses(hist, live.points)
	var prior []closePoint // the days before the current one
	for _, p := range points {
		if p.day < today {
			prior = append(prior, p)
		}
	}
	r := &Quote{Symbol: asked, Source: src, Name: live.name, Price: round6(live.price), Currency: live.currency}
	if !live.at.IsZero() {
		t := live.at.UTC()
		r.Time = &t
	}
	if n := len(prior); n > 0 {
		prev := prior[n-1].close
		r.PreviousClose = round6(prev)
		r.Change = round6(live.price - prev)
		if prev != 0 {
			r.ChangePercent = round6((live.price - prev) / prev * 100)
		}
	}
	closes := make([]float64, 0, quoteCloses)
	if len(prior) > quoteCloses-1 {
		prior = prior[len(prior)-(quoteCloses-1):]
	}
	for _, p := range prior {
		closes = append(closes, round6(p.close))
	}
	r.Closes = append(closes, round6(live.price))
	return r
}

// goldCNY is gold in yuan per gram: the dollar price per ounce times the
// dollar's yuan rate, over grams per ounce, day by day.
func (q *QuoteService) goldCNY(ctx context.Context, asked string) (*Quote, error) {
	gold, stale1, err := q.liveChart(ctx, "GC=F")
	if err != nil {
		return nil, err
	}
	fx, stale2, err := q.liveChart(ctx, "CNY=X")
	if err != nil {
		return nil, err
	}
	g := assembleQuote("GC=F", "GC=F", gold, q.historyPoints(ctx, "GC=F"))
	fxHist := mergeCloses(q.historyPoints(ctx, "CNY=X"), fx.points)
	rate := map[string]float64{}
	for _, p := range fxHist {
		rate[p.day] = p.close
	}
	// The rate on a day the gold market traded and FX did not: the last one
	// before it.
	days := make([]string, 0, len(rate))
	for d := range rate {
		days = append(days, d)
	}
	sort.Strings(days)
	rateOn := func(day string) float64 {
		i := sort.SearchStrings(days, day)
		if i < len(days) && days[i] == day {
			return rate[day]
		}
		if i > 0 {
			return rate[days[i-1]]
		}
		return fx.price
	}
	goldDays := mergeCloses(q.historyPoints(ctx, "GC=F"), gold.points)
	today := gold.at.In(gold.zone).Format("2006-01-02")
	var closes []float64
	var prev float64
	for _, p := range goldDays {
		if p.day >= today {
			continue
		}
		v := p.close * rateOn(p.day) / gramsPerTroyOunce
		closes = append(closes, round6(v))
		prev = v
	}
	if len(closes) > quoteCloses-1 {
		closes = closes[len(closes)-(quoteCloses-1):]
	}
	price := gold.price * fx.price / gramsPerTroyOunce
	r := &Quote{Symbol: asked, Source: "GC=F*CNY=X/31.1035", Name: "黄金 (COMEX, CNY/g)", Price: round6(price),
		Currency: "CNY", Unit: "g", Time: g.Time, Stale: stale1 || stale2}
	if prev != 0 {
		r.PreviousClose = round6(prev)
		r.Change = round6(price - prev)
		r.ChangePercent = round6((price - prev) / prev * 100)
	}
	r.Closes = append(closes, round6(price))
	return r, nil
}

// liveChart is the 5-day chart, from the cache while it is fresh. When the
// source fails, the last good one is given as stale.
func (q *QuoteService) liveChart(ctx context.Context, src string) (chartData, bool, error) {
	now := q.now()
	q.mu.Lock()
	e, ok := q.live[src]
	q.mu.Unlock()
	if ok {
		if e.err != nil && errors.Is(e.err, ErrUnknownSymbol) && now.Sub(e.at) < quoteUnknownTTL {
			return chartData{}, false, e.err
		}
		if e.err == nil && now.Sub(e.at) < quoteLiveTTL {
			return e.chart, false, nil
		}
	}
	c, err := q.fetch(ctx, src, "5d")
	if err != nil {
		if errors.Is(err, ErrUnknownSymbol) {
			q.mu.Lock()
			q.live[src] = liveEntry{at: now, err: err}
			q.mu.Unlock()
			return chartData{}, false, err
		}
		if ok && e.err == nil {
			return e.chart, true, nil
		}
		return chartData{}, false, err
	}
	q.mu.Lock()
	q.live[src] = liveEntry{at: now, chart: c}
	q.mu.Unlock()
	return c, false, nil
}

// historyPoints is three months of closes, refreshed every few hours; nil
// when it cannot be had (the 5-day bars then carry the line alone).
func (q *QuoteService) historyPoints(ctx context.Context, src string) []closePoint {
	now := q.now()
	q.mu.Lock()
	e, ok := q.history[src]
	q.mu.Unlock()
	if ok && now.Sub(e.at) < quoteHistoryTTL {
		return e.points
	}
	c, err := q.fetch(ctx, src, "3mo")
	if err != nil {
		if ok {
			return e.points
		}
		return nil
	}
	q.mu.Lock()
	q.history[src] = historyEntry{at: now, points: c.points}
	q.mu.Unlock()
	return c.points
}

// fetch asks the source for one chart.
func (q *QuoteService) fetch(ctx context.Context, src, rng string) (chartData, error) {
	u := fmt.Sprintf("%s/v8/finance/chart/%s?range=%s&interval=1d", q.base, url.PathEscape(src), rng)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return chartData{}, err
	}
	// Without a browser's agent the source answers 429.
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15")
	req.Header.Set("Accept", "application/json")
	resp, err := q.client.Do(req)
	if err != nil {
		return chartData{}, fmt.Errorf("market data unreachable: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return chartData{}, err
	}
	c, perr := parseChart(body)
	switch {
	case resp.StatusCode == http.StatusNotFound || errors.Is(perr, ErrUnknownSymbol):
		return chartData{}, ErrUnknownSymbol
	case resp.StatusCode != http.StatusOK:
		return chartData{}, fmt.Errorf("market data answered %d", resp.StatusCode)
	case perr != nil:
		return chartData{}, perr
	}
	return c, nil
}

// parseChart reads one chart response.
func parseChart(body []byte) (chartData, error) {
	var doc struct {
		Chart struct {
			Result []struct {
				Meta struct {
					Symbol               string   `json:"symbol"`
					Currency             string   `json:"currency"`
					LongName             string   `json:"longName"`
					ShortName            string   `json:"shortName"`
					RegularMarketPrice   *float64 `json:"regularMarketPrice"`
					RegularMarketTime    int64    `json:"regularMarketTime"`
					ExchangeTimezoneName string   `json:"exchangeTimezoneName"`
					GMTOffset            int      `json:"gmtoffset"`
				} `json:"meta"`
				Timestamp  []int64 `json:"timestamp"`
				Indicators struct {
					Quote []struct {
						Close []*float64 `json:"close"`
					} `json:"quote"`
				} `json:"indicators"`
			} `json:"result"`
			Error *struct {
				Code        string `json:"code"`
				Description string `json:"description"`
			} `json:"error"`
		} `json:"chart"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return chartData{}, fmt.Errorf("market data: %w", err)
	}
	if e := doc.Chart.Error; e != nil {
		if strings.EqualFold(e.Code, "Not Found") || strings.Contains(strings.ToLower(e.Description), "no data") {
			return chartData{}, ErrUnknownSymbol
		}
		return chartData{}, fmt.Errorf("market data: %s", e.Description)
	}
	if len(doc.Chart.Result) == 0 {
		return chartData{}, ErrUnknownSymbol
	}
	r := doc.Chart.Result[0]
	m := r.Meta
	zone, err := time.LoadLocation(m.ExchangeTimezoneName)
	if err != nil || m.ExchangeTimezoneName == "" {
		zone = time.FixedZone("exchange", m.GMTOffset)
	}
	c := chartData{symbol: m.Symbol, currency: m.Currency, zone: zone, name: m.LongName}
	if c.name == "" {
		c.name = m.ShortName
	}
	var closes []*float64
	if len(r.Indicators.Quote) > 0 {
		closes = r.Indicators.Quote[0].Close
	}
	byDay := map[string]float64{}
	var order []string
	for i, ts := range r.Timestamp {
		if i >= len(closes) || closes[i] == nil || math.IsNaN(*closes[i]) {
			continue
		}
		d := time.Unix(ts, 0).In(zone).Format("2006-01-02")
		if _, ok := byDay[d]; !ok {
			order = append(order, d)
		}
		byDay[d] = *closes[i]
	}
	for _, d := range order {
		c.points = append(c.points, closePoint{day: d, close: byDay[d]})
	}
	switch {
	case m.RegularMarketPrice != nil:
		c.price = *m.RegularMarketPrice
	case len(c.points) > 0:
		c.price = c.points[len(c.points)-1].close
	default:
		return chartData{}, ErrUnknownSymbol
	}
	if m.RegularMarketTime > 0 {
		c.at = time.Unix(m.RegularMarketTime, 0)
	} else if n := len(r.Timestamp); n > 0 {
		c.at = time.Unix(r.Timestamp[n-1], 0)
	}
	return c, nil
}

// mergeCloses lays the newer points over the older, by day, in day order.
func mergeCloses(older, newer []closePoint) []closePoint {
	by := map[string]float64{}
	for _, p := range older {
		by[p.day] = p.close
	}
	for _, p := range newer {
		by[p.day] = p.close
	}
	out := make([]closePoint, 0, len(by))
	for d, v := range by {
		out = append(out, closePoint{day: d, close: v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].day < out[j].day })
	return out
}

func round6(v float64) float64 { return math.Round(v*1e6) / 1e6 }
