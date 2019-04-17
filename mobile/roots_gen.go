// +build ignore

// Generates roots_gen.go.
package main

import (
	"bytes"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"flag"
	"fmt"
	"go/format"
	"io/ioutil"
	"log"
	"net/http"
	"os/exec"
	"regexp"
	"strings"
)

var allowedCAs = map[string]bool{
	"CN=AddTrust Class 1 CA Root,OU=AddTrust TTP Network,O=AddTrust AB,C=SE":           true,
	"CN=AddTrust External CA Root,OU=AddTrust External TTP Network,O=AddTrust AB,C=SE": true,

	"CN=COMODO Certification Authority,O=COMODO CA Limited,L=Salford,ST=Greater Manchester,C=GB":     true,
	"CN=COMODO ECC Certification Authority,O=COMODO CA Limited,L=Salford,ST=Greater Manchester,C=GB": true,
	"CN=COMODO RSA Certification Authority,O=COMODO CA Limited,L=Salford,ST=Greater Manchester,C=GB": true,

	"CN=DigiCert Global Root CA,OU=www.digicert.com,O=DigiCert Inc,C=US":            true,
	"CN=DigiCert Global Root G2,OU=www.digicert.com,O=DigiCert Inc,C=US":            true,
	"CN=DigiCert Global Root G3,OU=www.digicert.com,O=DigiCert Inc,C=US":            true,
	"CN=DigiCert High Assurance EV Root CA,OU=www.digicert.com,O=DigiCert Inc,C=US": true,
	"CN=DigiCert Trusted Root G4,OU=www.digicert.com,O=DigiCert Inc,C=US":           true,

	"CN=DST Root CA X3,O=Digital Signature Trust Co.":         true,
	"CN=DST Root CA X4,O=Digital Signature Trust Co.":         true,
	"CN=ISRG Root X1,O=Internet Security Research Group,C=US": true,

	"CN=GlobalSign Root CA,OU=Root CA,O=GlobalSign nv-sa,C=BE":  true,
	"CN=GlobalSign,OU=GlobalSign ECC Root CA - R4,O=GlobalSign": true,
	"CN=GlobalSign,OU=GlobalSign ECC Root CA - R5,O=GlobalSign": true,
	"CN=GlobalSign,OU=GlobalSign Root CA - R2,O=GlobalSign":     true,
	"CN=GlobalSign,OU=GlobalSign Root CA - R3,O=GlobalSign":     true,

	`OU=Go Daddy Class 2 Certification Authority,O=The Go Daddy Group\, Inc.,C=US`:                  true,
	`CN=Go Daddy Root Certificate Authority - G2,O=GoDaddy.com\, Inc.,L=Scottsdale,ST=Arizona,C=US`: true,
}

var output = flag.String("output", "roots_list.go", "file name to write")

func main() {
	certs, err := selectCerts()
	if err != nil {
		log.Fatal(err)
	}

	buf := new(bytes.Buffer)

	fmt.Fprintf(buf, "// Code generated by roots_gen --output %s; DO NOT EDIT.\n", *output)
	fmt.Fprintf(buf, "%s", header)

	fmt.Fprintf(buf, "const systemRootsPEM = `\n")
	for _, cert := range certs {

		subjectName := cert.Subject.String()
		log.Printf(subjectName)

		if _, ok := allowedCAs[subjectName]; ok {
			b := &pem.Block{
				Type:  "CERTIFICATE",
				Bytes: cert.Raw,
			}
			if err := pem.Encode(buf, b); err != nil {
				log.Fatal(err)
			}
		}

	}
	fmt.Fprintf(buf, "`")

	source, err := format.Source(buf.Bytes())
	if err != nil {
		log.Fatal("source format error:", err)
	}
	if err := ioutil.WriteFile(*output, source, 0644); err != nil {
		log.Fatal(err)
	}
}

func selectCerts() ([]*x509.Certificate, error) {
	ids, err := fetchCertIDs()
	if err != nil {
		return nil, err
	}

	scerts, err := sysCerts()
	if err != nil {
		return nil, err
	}

	var certs []*x509.Certificate
	for _, id := range ids {
		if c, ok := scerts[id.fingerprint]; ok {
			certs = append(certs, c)
		} else {
			fmt.Printf("WARNING: cannot find certificate: %s (fingerprint: %s)\n", id.name, id.fingerprint)
		}
	}
	return certs, nil
}

func sysCerts() (certs map[string]*x509.Certificate, err error) {
	cmd := exec.Command("/usr/bin/security", "find-certificate", "-a", "-p", "/System/Library/Keychains/SystemRootCertificates.keychain")
	data, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	certs = make(map[string]*x509.Certificate)
	for len(data) > 0 {
		var block *pem.Block
		block, data = pem.Decode(data)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			continue
		}

		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			continue
		}

		fingerprint := sha256.Sum256(cert.Raw)
		certs[hex.EncodeToString(fingerprint[:])] = cert
	}
	return certs, nil
}

type certID struct {
	name        string
	fingerprint string
}

// fetchCertIDs fetches IDs of iOS X509 certificates from apple.com.
func fetchCertIDs() ([]certID, error) {
	// Download the iOS 11 support page. The index for all iOS versions is here:
	// https://support.apple.com/en-us/HT204132
	resp, err := http.Get("https://support.apple.com/en-us/HT208125")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	text := string(body)
	idx := strings.Index(text, "<div id=\"trusted\"")
	text = text[idx:]
	text = text[:strings.Index(text, "</div>")]

	var ids []certID
	cols := make(map[string]int)
	for i, rowmatch := range regexp.MustCompile("(?s)<tr>(.*?)</tr>").FindAllStringSubmatch(text, -1) {
		row := rowmatch[1]
		if i == 0 {
			// Parse table header row to extract column names
			for i, match := range regexp.MustCompile("(?s)<th>(.*?)</th>").FindAllStringSubmatch(row, -1) {
				cols[match[1]] = i
			}
			continue
		}

		values := regexp.MustCompile("(?s)<td>(.*?)</td>").FindAllStringSubmatch(row, -1)
		name := values[cols["Certificate name"]][1]
		name = strings.ReplaceAll(name, "&nbsp;", "")
		fingerprint := values[cols["Fingerprint (SHA-256)"]][1]
		fingerprint = strings.ReplaceAll(fingerprint, "<br>", "")
		fingerprint = strings.ReplaceAll(fingerprint, "\n", "")
		fingerprint = strings.ReplaceAll(fingerprint, " ", "")
		fingerprint = strings.ReplaceAll(fingerprint, "&nbsp;", "")
		fingerprint = strings.ToLower(fingerprint)

		ids = append(ids, certID{
			name:        name,
			fingerprint: fingerprint,
		})
	}
	return ids, nil
}

const header = `
package mobile

`
