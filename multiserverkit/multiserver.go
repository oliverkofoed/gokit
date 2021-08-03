package multiserverkit

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"

	"github.com/oliverkofoed/gokit/logkit"
	"github.com/soheilhy/cmux"
	"golang.org/x/crypto/acme/autocert"
)

type MultiServer struct {
	achmeChallengeHandler  http.Handler
	TlsConfig              *tls.Config
	GetSite                func(domain string) http.Handler
	HandleTCP              func(conn net.Conn)
	HandleGRPC             func(lis net.Listener)
	BasicHttpAuthenticator func(req *http.Request, username string, password string) (bool, string)
}

func New() *MultiServer {
	return NewWithAutocert(nil)
}

func NewWithAutocert(autocertCache autocert.Cache) *MultiServer {
	s := &MultiServer{
		GetSite: func(domain string) http.Handler {
			return nil
		},
		HandleTCP: func(conn net.Conn) {
			conn.Close()
		},
		BasicHttpAuthenticator: func(req *http.Request, username string, password string) (bool, string) {
			return true, ""
		},
	}

	if autocertCache != nil {
		m := autocert.Manager{
			Cache:  autocertCache, //newEncryptedAutocertCache(autocertEncryptionKey, newDBAutocertCache(db)),
			Prompt: autocert.AcceptTOS,
			HostPolicy: func(_ context.Context, host string) error {
				if site := s.GetSite(host); site != nil {
					return nil
				}
				return fmt.Errorf("acme/autocert: invalid host %q", host)
			},
		}
		s.TlsConfig = &tls.Config{
			GetCertificate: m.GetCertificate,
		}
		s.achmeChallengeHandler = m.HTTPHandler(nil)
	}

	return s
}

func (s *MultiServer) Listen(ctx context.Context, listen string, useTls bool) {
	// Create the main listener.
	logkit.Info(nil, "listning", logkit.String("addr", listen), logkit.Bool("tls", useTls))
	l, err := net.Listen("tcp", listen)
	if err != nil {
		panic(err)
	}

	if useTls {
		if s.TlsConfig != nil {
			l = tls.NewListener(l, s.TlsConfig)
		} else {
			panic(errors.New("Can't call .listen(ctx,listen,tls) with tls=true when there is no .TlsConfig on the Multiserver."))
		}
	}

	m := cmux.New(l)
	if s.HandleGRPC != nil {
		go s.HandleGRPC(m.MatchWithWriters(cmux.HTTP2MatchHeaderFieldSendSettings("content-type", "application/grpc")))
	}
	go s.handleHttp(m.Match(cmux.HTTP2()))
	go s.handleHttp(m.Match(cmux.HTTP1Fast()))
	go s.handleTcp(m.Match(cmux.Any()))

	if err := m.Serve(); !strings.Contains(err.Error(), "use of closed network connection") {
		panic(err)
	}
}

func (s *MultiServer) handleHttp(l net.Listener) {
	if err := http.Serve(l, s); err != cmux.ErrListenerClosed {
		panic(err)
	}
}

func (s *MultiServer) handleTcp(l net.Listener) {
	for {
		conn, err := l.Accept()
		if err != nil {
			log.Printf("unable to accept connection: %+v", err)
			continue
		}
		s.HandleTCP(conn)
	}
}

func (s *MultiServer) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	// find the tld-domain
	parts := strings.Split(req.Host, ":")
	domain := parts[0]
	//parts = strings.Split(parts[0], ".")
	//if len(parts) > 2 {
	//domain = parts[len(parts)-2] + "." + parts[len(parts)-1]
	//}

	// find the right site
	site := s.GetSite(domain)
	if site == nil {
		http.NotFound(w, req)
		return
	}

	// handle lets encrypt requests
	if s.achmeChallengeHandler != nil && strings.HasPrefix(req.URL.Path, "/.well-known/acme-challenge/") {
		s.achmeChallengeHandler.ServeHTTP(w, req)
		return
	}

	if site != nil {
		// basic authentication?
		username, password, _ := req.BasicAuth()
		allow, realm := s.BasicHttpAuthenticator(req, username, password)
		if !allow {
			w.Header().Set("WWW-Authenticate", fmt.Sprintf("Basic realm=\"%v\"", realm))
			w.WriteHeader(401)
			w.Write([]byte("Unauthorised.\n"))
			return
		}

		site.ServeHTTP(w, req)
	} else {
		http.NotFound(w, req)
	}
}
