// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"strconv"
	"time"
)

func main() {
	if os.Getenv("AIVEN_NPM_TEST_CERTIFICATE") == "1" {
		if err := makeCertificate(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if os.Getenv("AIVEN_NPM_TEST_FLOOD") == "1" {
		for {
			fmt.Println("ready")
		}
	}
	if os.Getenv("AIVEN_NPM_TEST_WAIT") == "1" {
		fmt.Println("ready")
		_, _ = io.Copy(io.Discard, os.Stdin)
		return
	}
	input, _ := io.ReadAll(os.Stdin)
	result := struct {
		Argv           []string
		Input          string
		Env            string
		GatewayKey     *string `json:",omitempty"`
		GatewayKeySet  *bool   `json:",omitempty"`
		AnthropicToken *string `json:",omitempty"`
	}{
		Argv: os.Args[1:], Input: string(input), Env: os.Getenv("AIVEN_NPM_TEST_ENV"),
	}
	if os.Getenv("AIVEN_NPM_TEST_CAPTURE_AUTH") == "1" {
		key, present := os.LookupEnv("AIVEN_AI_GATEWAY_KEY")
		token := os.Getenv("ANTHROPIC_AUTH_TOKEN")
		result.GatewayKey, result.GatewayKeySet, result.AnthropicToken = &key, &present, &token
	}
	_ = json.NewEncoder(os.Stdout).Encode(result)
	fmt.Fprint(os.Stderr, "fixture stderr")
	code, _ := strconv.Atoi(os.Getenv("AIVEN_NPM_TEST_STATUS"))
	os.Exit(code)
}

// makeCertificate creates a short-lived test CA and loopback server certificate in memory.
func makeCertificate() error {
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serverKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	now := time.Now()
	ca := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "npm smoke CA"},
		NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	if err != nil {
		return err
	}
	server := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "127.0.0.1"},
		NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour),
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage:    x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	serverDER, err := x509.CreateCertificate(rand.Reader, server, ca, &serverKey.PublicKey, caKey)
	if err != nil {
		return err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(serverKey)
	if err != nil {
		return err
	}
	asPEM := func(kind string, data []byte) string {
		return string(pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: data}))
	}
	return json.NewEncoder(os.Stdout).Encode(struct {
		CA, Certificate, PrivateKey string
	}{asPEM("CERTIFICATE", caDER), asPEM("CERTIFICATE", serverDER), asPEM("PRIVATE KEY", keyDER)})
}
