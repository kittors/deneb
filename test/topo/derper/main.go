// Command derper is the colima topology's relay: Tailscale's DERP server and
// a STUN server, the same code as the derper on hk-spare, with a
// self-signed certificate that the test engines trust through -relay-ca.
package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"flag"
	"log"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"tailscale.com/derp/derpserver"
	"tailscale.com/net/stunserver"
	"tailscale.com/types/key"
)

func main() {
	addr := flag.String("addr", ":443", "DERP over HTTPS")
	stunAddr := flag.String("stun", ":3478", "STUN over UDP")
	host := flag.String("hostname", "derper.deneb.test", "TLS name")
	dir := flag.String("cert-dir", "/certs", "where the certificate is written (derper.pem) for the engines to trust")
	flag.Parse()

	cert, err := selfSigned(*host, *dir)
	if err != nil {
		log.Fatal(err)
	}
	s := derpserver.New(key.NewNode(), log.Printf)
	mux := http.NewServeMux()
	mux.Handle("/derp", derpserver.Handler(s))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("deneb test derper\n")) })
	go func() {
		st := stunserver.New(context.Background())
		log.Fatal(st.ListenAndServe(*stunAddr))
	}()
	srv := &http.Server{Addr: *addr, Handler: mux, TLSConfig: &tls.Config{Certificates: []tls.Certificate{cert}}}
	log.Printf("derper on %s (TLS %s), STUN on %s", *addr, *host, *stunAddr)
	log.Fatal(srv.ListenAndServeTLS("", ""))
}

// selfSigned makes a certificate that is its own CA, and writes it where
// the engines read it.
func selfSigned(host, dir string) (tls.Certificate, error) {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: host},
		DNSNames:              []string{host},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(30 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &k.PublicKey, k)
	if err != nil {
		return tls.Certificate{}, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return tls.Certificate{}, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(filepath.Join(dir, "derper.pem"), certPEM, 0o644); err != nil {
		return tls.Certificate{}, err
	}
	kb, _ := x509.MarshalECPrivateKey(k)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb})
	return tls.X509KeyPair(certPEM, keyPEM)
}
