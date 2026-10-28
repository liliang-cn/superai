// superai-relay puts a phone through to a SuperLeo on a computer it shares no
// network with. It reads the name a phone's TLS asks for and copies bytes; the
// TLS itself is between the phone and the computer (internal/relay).
//
//	superai-relay -addr :43443 -domain relay.superleo.app
//
// The domain needs a wildcard DNS record, *.relay.superleo.app, pointing here.
package main

import (
	"flag"
	_ "github.com/liliang-cn/superleo/internal/brand" // the old SuperAI names keep working
	"log"
	"net"

	"github.com/liliang-cn/superleo/internal/relay"
)

func main() {
	addr := flag.String("addr", ":"+relay.DefaultPort, "where phones and computers connect")
	domain := flag.String("domain", relay.DefaultHost, "phones reach a computer at <id>.<domain>")
	flag.Parse()

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("superai-relay: %s for *.%s", ln.Addr(), *domain)
	log.Fatal((&relay.Server{Domain: *domain}).Serve(ln))
}
