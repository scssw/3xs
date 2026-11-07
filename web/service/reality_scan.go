package service

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mhsanaei/3x-ui/v2/logger"
	"github.com/mhsanaei/3x-ui/v2/util/netsafe"
)

const (
	realityScanTimeout     = 10 * time.Second
	realityDiscoverTimeout = 4 * time.Second
	realityScanConcurrency = 32
	realityDiscoverMaxIPs  = 256
	realityScanMaxTotal    = 512
)

var defaultRealityScanCandidatesUSWest = []string{
	"www.amd.com:443",
	"www.nvidia.com:443",
	"www.intel.com:443",
	"www.oracle.com:443",
	"www.cisco.com:443",
	"www.tesla.com:443",
	"www.amazon.com:443",
	"www.ebay.com:443",
	"www.paypal.com:443",
	"www.adobe.com:443",
	"addons.mozilla.org:443",
	"www.stanford.edu:443",
}

var defaultRealityScanCandidatesTokyo = []string{
	"www.sony.jp:443",
	"www.nintendo.co.jp:443",
	"www.rakuten.co.jp:443",
	"www.u-tokyo.ac.jp:443",
	"www.ntt.co.jp:443",
}

var defaultRealityScanCandidates = append(
	append([]string(nil), defaultRealityScanCandidatesUSWest...),
	defaultRealityScanCandidatesTokyo...,
)

// DefaultRealityScanCandidatesCSV is the comma-separated host:port list.
var DefaultRealityScanCandidatesCSV = strings.Join(defaultRealityScanCandidates, ",")

// RealityScanResult holds the result of probing a REALITY candidate.
type RealityScanResult struct {
	Target         string   `json:"target"`
	Host           string   `json:"host"`
	IP             string   `json:"ip"`
	Port           int      `json:"port"`
	Region         string   `json:"region"`
	Feasible       bool     `json:"feasible"`
	PrivateTarget  bool     `json:"privateTarget"`
	TLS13          bool     `json:"tls13"`
	TLSVersion     string   `json:"tlsVersion"`
	H2             bool     `json:"h2"`
	ALPN           string   `json:"alpn"`
	X25519         bool     `json:"x25519"`
	CurveID        string   `json:"curveID"`
	CertValid      bool     `json:"certValid"`
	CertChainValid bool     `json:"certChainValid"`
	CertChainBytes int      `json:"certChainBytes"`
	CertSubject    string   `json:"certSubject"`
	CertIssuer     string   `json:"certIssuer"`
	NotAfter       string   `json:"notAfter"`
	ServerNames    []string `json:"serverNames"`
	LatencyMs      int      `json:"latencyMs"`
	Reason         string   `json:"reason"`
}

type realityProbeTask struct {
	dialHost string
	port     int
	sni      string
	timeout  time.Duration
	bulk     bool
}

func tlsVersionName(v uint16) string {
	switch v {
	case tls.VersionTLS13:
		return "1.3"
	case tls.VersionTLS12:
		return "1.2"
	case tls.VersionTLS11:
		return "1.1"
	case tls.VersionTLS10:
		return "1.0"
	default:
		return "unknown"
	}
}

func realityCurveName(id tls.CurveID) string {
	switch id {
	case tls.X25519:
		return "X25519"
	case tls.X25519MLKEM768:
		return "X25519MLKEM768"
	case tls.CurveP256:
		return "P-256"
	case tls.CurveP384:
		return "P-384"
	case tls.CurveP521:
		return "P-521"
	default:
		return "unknown"
	}
}

func judgeRealityRegion(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	for _, cand := range defaultRealityScanCandidatesTokyo {
		h, _, _ := net.SplitHostPort(cand)
		if h == "" {
			h = cand
		}
		if host == strings.ToLower(h) || strings.HasSuffix(host, ".jp") || strings.HasSuffix(host, ".co.jp") || strings.HasSuffix(host, ".ac.jp") {
			return "tokyo"
		}
	}
	for _, cand := range defaultRealityScanCandidatesUSWest {
		h, _, _ := net.SplitHostPort(cand)
		if h == "" {
			h = cand
		}
		if host == strings.ToLower(h) {
			return "us-west"
		}
	}
	return "us-west"
}

// filterUsableSANs strips root/apex domains when dialHost is a subdomain (e.g. www.amd.com -> strips amd.com).
func filterUsableSANs(dnsNames []string, dialHost ...string) []string {
	var targetSubdomain string
	var targetApex string
	if len(dialHost) > 0 {
		h := strings.ToLower(strings.TrimSpace(dialHost[0]))
		if strings.HasPrefix(h, "www.") && len(h) > 4 {
			targetSubdomain = h
			targetApex = h[4:]
		}
	}

	seen := make(map[string]struct{}, len(dnsNames))
	out := make([]string, 0, len(dnsNames))
	for _, n := range dnsNames {
		n = strings.ToLower(strings.TrimSpace(n))
		if n == "" || strings.HasPrefix(n, "*") {
			continue
		}
		if targetApex != "" && n == targetApex {
			continue
		}
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}

	if targetSubdomain != "" {
		if _, ok := seen[targetSubdomain]; !ok {
			out = append([]string{targetSubdomain}, out...)
		} else {
			idx := slices.Index(out, targetSubdomain)
			if idx > 0 {
				out = append([]string{targetSubdomain}, append(out[:idx], out[idx+1:]...)...)
			}
		}
	}
	return out
}

func parseRealityTarget(raw string) (string, int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", 0, errors.New("target is required")
	}
	var hostStr string
	port := 443
	if h, p, err := net.SplitHostPort(raw); err == nil {
		hostStr = h
		parsedPort, perr := strconv.Atoi(p)
		if perr != nil || parsedPort <= 0 || parsedPort > 65535 {
			return "", 0, fmt.Errorf("invalid port in target %q", raw)
		}
		port = parsedPort
	} else {
		hostStr = raw
	}
	normalized, err := netsafe.NormalizeHost(hostStr)
	if err != nil {
		return "", 0, err
	}
	return normalized, port, nil
}

// ScanRealityTarget probes a single candidate target.
func (s *ServerService) ScanRealityTarget(target, sni string, xver int, allowPrivate bool) (*RealityScanResult, error) {
	host, port, err := parseRealityTarget(target)
	if err != nil {
		return nil, err
	}
	sni = strings.TrimSpace(sni)
	if sni == "" {
		sni = host
	} else {
		normalizedSni, serr := netsafe.NormalizeHost(sni)
		if serr != nil {
			return nil, fmt.Errorf("invalid SNI: %w", serr)
		}
		sni = normalizedSni
	}

	ctx := netsafe.ContextWithAllowPrivate(context.Background(), allowPrivate)
	res := probeRealityTarget(ctx, realityProbeTask{
		dialHost: host,
		port:     port,
		sni:      sni,
		timeout:  realityScanTimeout,
		bulk:     false,
	}, xver)

	return res, nil
}

// ScanRealityTargets probes multiple candidates.
func (s *ServerService) ScanRealityTargets(targetsCSV string) ([]RealityScanResult, error) {
	targetsCSV = strings.TrimSpace(targetsCSV)
	var tokens []string
	switch targetsCSV {
	case "us-west":
		tokens = defaultRealityScanCandidatesUSWest
	case "tokyo":
		tokens = defaultRealityScanCandidatesTokyo
	case "all", "":
		tokens = defaultRealityScanCandidates
	default:
		tokens = parseRealityScanCandidateCSV(targetsCSV)
		if len(tokens) == 0 {
			tokens = defaultRealityScanCandidates
		}
	}

	tasks := make([]realityProbeTask, 0, len(tokens))
	for _, tok := range tokens {
		host, port, perr := parseRealityTarget(tok)
		if perr != nil {
			continue
		}
		tasks = append(tasks, realityProbeTask{
			dialHost: host,
			port:     port,
			sni:      host,
			timeout:  realityDiscoverTimeout,
			bulk:     true,
		})
	}

	results := probeRealityTasksConcurrently(tasks, 0)
	rankRealityScanResults(results)
	return results, nil
}

func parseRealityScanCandidateCSV(csv string) []string {
	parts := strings.Split(csv, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func probeRealityTasksConcurrently(tasks []realityProbeTask, xver int) []RealityScanResult {
	if len(tasks) == 0 {
		return nil
	}
	results := make([]RealityScanResult, len(tasks))
	var wg sync.WaitGroup
	sem := make(chan struct{}, realityScanConcurrency)

	for i, task := range tasks {
		wg.Add(1)
		go func(idx int, t realityProbeTask) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			ctx := context.Background()
			res := probeRealityTarget(ctx, t, xver)
			if res != nil {
				results[idx] = *res
			}
		}(i, task)
	}

	wg.Wait()
	return results
}

func rankRealityScanResults(results []RealityScanResult) {
	slices.SortStableFunc(results, func(a, b RealityScanResult) int {
		if a.Feasible != b.Feasible {
			if a.Feasible {
				return -1
			}
			return 1
		}
		if a.H2 != b.H2 {
			if a.H2 {
				return -1
			}
			return 1
		}
		if a.TLS13 != b.TLS13 {
			if a.TLS13 {
				return -1
			}
			return 1
		}
		return a.LatencyMs - b.LatencyMs
	})
}

func probeRealityTarget(ctx context.Context, task realityProbeTask, xver int) *RealityScanResult {
	target := net.JoinHostPort(task.dialHost, strconv.Itoa(task.port))
	res := &RealityScanResult{
		Target:      target,
		Host:        task.dialHost,
		Port:        task.port,
		Region:      judgeRealityRegion(task.dialHost),
		ServerNames: []string{task.sni},
	}

	start := time.Now()
	conn, err := netsafe.SSRFGuardedDialContext(ctx, "tcp", target)
	if err != nil {
		res.Reason = fmt.Sprintf("dial failed: %v", err)
		if errors.Is(err, netsafe.ErrPrivateAddressBlocked) {
			res.PrivateTarget = true
		}
		return res
	}
	defer conn.Close()

	if tcpConn, ok := conn.(*net.TCPConn); ok {
		if rAddr := tcpConn.RemoteAddr(); rAddr != nil {
			if host, _, err := net.SplitHostPort(rAddr.String()); err == nil {
				res.IP = host
			}
		}
	}

	tlsConfig := &tls.Config{
		ServerName:         task.sni,
		NextProtos:         []string{"h2", "http/1.1"},
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: true,
	}

	tlsConn := tls.Client(conn, tlsConfig)
	_ = tlsConn.SetDeadline(time.Now().Add(task.timeout))
	handshakeErr := tlsConn.Handshake()
	res.LatencyMs = int(time.Since(start).Milliseconds())

	if handshakeErr != nil {
		res.Reason = fmt.Sprintf("handshake failed: %v", handshakeErr)
		return res
	}

	state := tlsConn.ConnectionState()
	res.TLSVersion = tlsVersionName(state.Version)
	res.TLS13 = state.Version == tls.VersionTLS13
	res.ALPN = state.NegotiatedProtocol
	res.H2 = state.NegotiatedProtocol == "h2"
	res.CurveID = realityCurveName(state.CurveID)
	res.X25519 = state.CurveID == tls.X25519 || state.CurveID == tls.X25519MLKEM768

	if len(state.PeerCertificates) > 0 {
		leaf := state.PeerCertificates[0]
		res.CertSubject = leaf.Subject.CommonName
		res.CertIssuer = leaf.Issuer.CommonName
		res.NotAfter = leaf.NotAfter.Format(time.RFC3339)
		res.ServerNames = filterUsableSANs(append([]string{leaf.Subject.CommonName}, leaf.DNSNames...), task.dialHost)

		roots, _ := x509.SystemCertPool()
		opts := x509.VerifyOptions{
			DNSName:       task.sni,
			Roots:         roots,
			Intermediates: x509.NewCertPool(),
		}
		for _, cert := range state.PeerCertificates[1:] {
			opts.Intermediates.AddCert(cert)
		}
		if _, err := leaf.Verify(opts); err == nil {
			res.CertValid = true
			res.CertChainValid = true
		} else {
			opts.DNSName = ""
			if _, chainErr := leaf.Verify(opts); chainErr == nil {
				res.CertChainValid = true
			}
		}

		totalBytes := 0
		for _, cert := range state.PeerCertificates {
			totalBytes += len(cert.Raw)
		}
		res.CertChainBytes = totalBytes
	}

	if res.TLS13 && res.H2 && res.CertChainValid {
		res.Feasible = true
	} else {
		var reasons []string
		if !res.TLS13 {
			reasons = append(reasons, "TLS 1.3 unsupported")
		}
		if !res.H2 {
			reasons = append(reasons, "H2 ALPN unsupported")
		}
		if !res.CertChainValid {
			reasons = append(reasons, "invalid certificate chain")
		}
		res.Reason = strings.Join(reasons, ", ")
	}

	return res
}
