// superleo-relay puts a phone through to a SuperLeo on a computer it shares no
// network with. It reads the name a phone's TLS asks for and copies bytes; the
// TLS itself is between the phone and the computer (internal/relay).
//
//	superleo-relay -addr :43443 -domain relay.superleo.app
//
// The domain needs a wildcard DNS record, *.relay.superleo.app, pointing here.
package main

import (
	"flag"
	_ "github.com/liliang-cn/superleo/internal/brand" // the old SuperLeo names keep working
	"log/slog"
	"net"
	"os"

	"github.com/liliang-cn/superleo/internal/relay"
)

func main() {
	addr := flag.String("addr", ":"+relay.DefaultPort, "where phones and computers connect")
	domain := flag.String("domain", relay.DefaultHost, "phones reach a computer at <id>.<domain>")
	flag.Parse()

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		slog.Error("cannot listen", "addr", *addr, "error", err)
		os.Exit(1)
	}
	slog.Info("relay listening", "addr", ln.Addr().String(), "domain", "*."+*domain)
	err = (&relay.Server{Domain: *domain}).Serve(ln)
	slog.Error("serve", "error", err)
	os.Exit(1)
}
