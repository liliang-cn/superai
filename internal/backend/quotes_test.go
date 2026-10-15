package backend

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// chartJSON is a chart response in the source's shape: daily bars at 13:30Z
// (09:30 New York), the last one today's, and the market price at its time.
func chartJSON(symbol, currency, tz string, gmtoff int, price float64, marketTime int64, days []int64, closes []string) string {
	ts := make([]string, len(days))
	for i, d := range days {
		ts[i] = fmt.Sprint(d)
	}
	return fmt.Sprintf(`{"chart":{"result":[{"meta":{"currency":%q,"symbol":%q,"longName":"Name of %s","regularMarketPrice":%v,
"regularMarketTime":%d,"exchangeTimezoneName":%q,"gmtoffset":%d},"timestamp":[%s],
"indicators":{"quote":[{"close":[%s]}]}}],"error":null}}`,
		currency, symbol, symbol, price, marketTime, tz, gmtoff, strings.Join(ts, ","), strings.Join(closes, ","))
}

const notFoundJSON = `{"chart":{"result":null,"error":{"code":"Not Found","description":"No data found, symbol may be delisted"}}}`

// day is 13:30Z on 2026-10-dd.
func day(dd int) int64 { return time.Date(2026, 10, dd, 13, 30, 0, 0, time.UTC).Unix() }

type stubSource struct {
	mu    sync.Mutex
	calls map[string]int
	fail  bool
}

func (s *stubSource) count(k string) int { s.mu.Lock(); defer s.mu.Unlock(); return s.calls[k] }

func newStubSource(t *testing.T) (*stubSource, *httptest.Server) {
	st := &stubSource{calls: map[string]int{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sym := strings.TrimPrefix(r.URL.Path, "/v8/finance/chart/")
		rng := r.URL.Query().Get("range")
		st.mu.Lock()
		st.calls[sym+" "+rng]++
		fail := st.fail
		st.mu.Unlock()
		if r.Header.Get("User-Agent") == "" {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		if fail {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		ny := "America/New_York"
		switch sym + " " + rng {
		case "VOO 5d":
			// Today is the 14th; the market time is its close.
			fmt.Fprint(w, chartJSON("VOO", "USD", ny, -14400, 707.43, time.Date(2026, 10, 14, 20, 0, 0, 0, time.UTC).Unix(),
				[]int64{day(8), day(9), day(12), day(13), day(14)}, []string{"700", "701", "715.13", "715.2", "707.43"}))
		case "VOO 3mo":
			// History from earlier, with a hole the source leaves as null.
			fmt.Fprint(w, chartJSON("VOO", "USD", ny, -14400, 700, time.Date(2026, 10, 9, 20, 0, 0, 0, time.UTC).Unix(),
				[]int64{day(1), day(2), day(5), day(6), day(7), day(8), day(9)}, []string{"690", "null", "692", "693", "694", "699", "701"}))
		case "BTC-USD 5d", "BTC-USD 3mo":
			fmt.Fprint(w, chartJSON("BTC-USD", "USD", "UTC", 0, 65000, time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC).Unix(),
				[]int64{time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC).Unix(), time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC).Unix()},
				[]string{"64000", "65000"}))
		case "GC=F 5d", "GC=F 3mo":
			fmt.Fprint(w, chartJSON("GC=F", "USD", ny, -14400, 3110.35, time.Date(2026, 10, 14, 20, 0, 0, 0, time.UTC).Unix(),
				[]int64{day(12), day(13), day(14)}, []string{"3000", "3045", "3110.35"}))
		case "CNY=X 5d", "CNY=X 3mo":
			// FX did not trade on the 13th; its rate on the 12th stands.
			fmt.Fprint(w, chartJSON("CNY=X", "CNY", "Europe/London", 3600, 7.2, time.Date(2026, 10, 14, 20, 0, 0, 0, time.UTC).Unix(),
				[]int64{day(12), day(14)}, []string{"7.1", "7.2"}))
		default:
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, notFoundJSON)
		}
	}))
	t.Cleanup(srv.Close)
	return st, srv
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-4 }

func TestQuoteDailyChangeAndCloses(t *testing.T) {
	st, srv := newStubSource(t)
	q := NewQuoteService(srv.URL)
	r, err := q.Quote(context.Background(), " voo ")
	if err != nil {
		t.Fatal(err)
	}
	if r.Symbol != "VOO" || r.Source != "VOO" || r.Currency != "USD" || r.Name != "Name of VOO" {
		t.Fatalf("%+v", r)
	}
	// Today's bar is today's: the previous close is the 13th's.
	if !near(r.Price, 707.43) || !near(r.PreviousClose, 715.2) || !near(r.Change, -7.77) || !near(r.ChangePercent, -1.086409) {
		t.Fatalf("change: %+v", r)
	}
	// History and the 5-day bars merged by day, the null skipped, today as the price.
	want := []float64{690, 692, 693, 694, 700, 701, 715.13, 715.2, 707.43} // the 5-day bar wins the 8th
	if fmt.Sprint(r.Closes) != fmt.Sprint(want) {
		t.Fatalf("closes %v, want %v", r.Closes, want)
	}
	if r.Time == nil || !r.Time.Equal(time.Date(2026, 10, 14, 20, 0, 0, 0, time.UTC)) {
		t.Fatalf("time %v", r.Time)
	}
	if st.count("VOO 5d") != 1 || st.count("VOO 3mo") != 1 {
		t.Fatalf("calls %v", st.calls)
	}
}

func TestQuoteCachesAndFallsBackWhenTheSourceFails(t *testing.T) {
	st, srv := newStubSource(t)
	q := NewQuoteService(srv.URL)
	now := time.Date(2026, 10, 14, 21, 0, 0, 0, time.UTC)
	q.now = func() time.Time { return now }
	ctx := context.Background()
	if _, err := q.Quote(ctx, "VOO"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(30 * time.Second)
	if _, err := q.Quote(ctx, "VOO"); err != nil {
		t.Fatal(err)
	}
	if st.count("VOO 5d") != 1 {
		t.Fatalf("asked again inside a minute: %v", st.calls)
	}
	now = now.Add(time.Minute)
	if _, err := q.Quote(ctx, "VOO"); err != nil {
		t.Fatal(err)
	}
	if st.count("VOO 5d") != 2 || st.count("VOO 3mo") != 1 {
		t.Fatalf("price not refreshed after a minute, or history refetched: %v", st.calls)
	}
	// The source goes down: the last answer, marked stale.
	st.mu.Lock()
	st.fail = true
	st.mu.Unlock()
	now = now.Add(2 * time.Minute)
	r, err := q.Quote(ctx, "VOO")
	if err != nil || !r.Stale || !near(r.Price, 707.43) {
		t.Fatalf("fallback: %+v %v", r, err)
	}
	// Nothing to fall back on: an error that is not "unknown symbol".
	if _, err := q.Quote(ctx, "BTC"); err == nil || errors.Is(err, ErrUnknownSymbol) {
		t.Fatalf("down with nothing cached: %v", err)
	}
}

func TestQuoteUnknownSymbolIsRememberedAndReported(t *testing.T) {
	st, srv := newStubSource(t)
	q := NewQuoteService(srv.URL)
	ctx := context.Background()
	if err := q.Check(ctx, "NOPE123"); !errors.Is(err, ErrUnknownSymbol) {
		t.Fatalf("check: %v", err)
	}
	_ = q.Check(ctx, "NOPE123")
	if st.count("NOPE123 5d") != 1 {
		t.Fatalf("an unknown symbol was asked about twice: %v", st.calls)
	}
	qs, bad := q.Quotes(ctx, []string{"VOO", "nope123", "voo", "BTC", ""})
	if len(qs) != 2 || qs[0].Symbol != "VOO" || qs[1].Symbol != "BTC" || qs[1].Source != "BTC-USD" {
		t.Fatalf("quotes %+v", qs)
	}
	if bad["NOPE123"] != "unknown symbol" || len(bad) != 1 {
		t.Fatalf("errors %v", bad)
	}
	// BTC: a 24-hour market whose day is the UTC day.
	if !near(qs[1].Change, 1000) || fmt.Sprint(qs[1].Closes) != "[64000 65000]" {
		t.Fatalf("btc %+v", qs[1])
	}
}

func TestQuoteGoldInCNYPerGram(t *testing.T) {
	_, srv := newStubSource(t)
	q := NewQuoteService(srv.URL)
	r, err := q.Quote(context.Background(), "黄金")
	if err != nil {
		t.Fatal(err)
	}
	price := 3110.35 * 7.2 / gramsPerTroyOunce
	prev := 3045 * 7.1 / gramsPerTroyOunce // the 12th's rate carries over to the 13th
	if r.Symbol != "黄金" || r.Currency != "CNY" || r.Unit != "g" || !near(r.Price, price) || !near(r.PreviousClose, prev) {
		t.Fatalf("%+v", r)
	}
	if len(r.Closes) != 3 || !near(r.Closes[0], 3000*7.1/gramsPerTroyOunce) || !near(r.Closes[2], price) {
		t.Fatalf("closes %v", r.Closes)
	}
}

func TestQuoteSourceSymbol(t *testing.T) {
	for in, want := range map[string]string{
		"btc": "BTC-USD", "ETH": "ETH-USD", "BTC-USD": "BTC-USD", "voo": "VOO", "GC=F": "GC=F", "^gspc": "^GSPC",
		"600519": "600519.SS", "510300": "510300.SS", "000001": "000001.SZ", "300750": "300750.SZ",
		"700": "0700.HK", "00700": "0700.HK", "9988": "9988.HK", "黄金": goldCNYPerGram, "AU9999": goldCNYPerGram,
		"xau": "GC=F", "GOLD": "GOLD",
	} {
		if got := QuoteSourceSymbol(in); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
}

func TestParseChartToleratesMissingParts(t *testing.T) {
	// No market price: the last close stands in.
	c, err := parseChart([]byte(`{"chart":{"result":[{"meta":{"currency":"USD","exchangeTimezoneName":"Nowhere/Else","gmtoffset":3600},
"timestamp":[1791984600],"indicators":{"quote":[{"close":[12.5]}]}}]}}`))
	if err != nil || c.price != 12.5 || len(c.points) != 1 {
		t.Fatalf("%+v %v", c, err)
	}
	if _, err := parseChart([]byte(`{"chart":{"result":[]}}`)); !errors.Is(err, ErrUnknownSymbol) {
		t.Fatalf("empty result: %v", err)
	}
	if _, err := parseChart([]byte(`not json`)); err == nil || errors.Is(err, ErrUnknownSymbol) {
		t.Fatalf("garbage: %v", err)
	}
}
