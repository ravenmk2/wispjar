package search

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/JohannesKaufmann/html-to-markdown/v2/converter"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/base"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/commonmark"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/table"
	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
)

var errBlockedTarget = errors.New("blocked fetch target")

type lookupFunc func(context.Context, string) ([]net.IPAddr, error)

var netLookup lookupFunc = net.DefaultResolver.LookupIPAddr

// In addition to RFC1918, reject shared, reserved, transition and documentation
// ranges. Unmap IPv4-mapped IPv6 before checking. IPv6 allows global unicast only.
var blockedPrefixes = func() []netip.Prefix {
	out := []netip.Prefix{}
	for _, s := range []string{"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12", "192.0.0.0/24", "192.0.2.0/24", "192.88.99.0/24", "192.168.0.0/16", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "224.0.0.0/3", "2001::/23", "2001:db8::/32", "2002::/16", "3fff::/20"} {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}()

func publicIP(ip net.IP) bool {
	a, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	a = a.Unmap()
	if !a.IsGlobalUnicast() || a.IsPrivate() || a.IsLoopback() || a.IsLinkLocalUnicast() {
		return false
	}
	if a.Is6() && !netip.MustParsePrefix("2000::/3").Contains(a) {
		return false
	}
	for _, p := range blockedPrefixes {
		if p.Contains(a) {
			return false
		}
	}
	return true
}
func publicAddresses(ctx context.Context, host string, lookup lookupFunc) ([]net.IPAddr, error) {
	h := strings.TrimSuffix(strings.ToLower(host), ".")
	if h == "localhost" || strings.HasSuffix(h, ".localhost") || strings.Contains(h, "%") || !strings.Contains(h, ".") && net.ParseIP(h) == nil {
		return nil, errBlockedTarget
	}
	if ip := net.ParseIP(h); ip != nil {
		if !publicIP(ip) {
			return nil, errBlockedTarget
		}
		return []net.IPAddr{{IP: ip}}, nil
	}
	ips, err := lookup(ctx, h)
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, errors.New("empty DNS answer")
	}
	for _, ip := range ips {
		if ip.Zone != "" || !publicIP(ip.IP) {
			return nil, errBlockedTarget
		}
	}
	return ips, nil
}
func safeDialContext(lookup lookupFunc, dial func(context.Context, string, string) (net.Conn, error)) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, errBlockedTarget
		}
		if port != "80" && port != "443" {
			return nil, errBlockedTarget
		}
		ips, err := publicAddresses(ctx, host, lookup)
		if err != nil {
			return nil, err
		}
		// One attempt, pinned to the checked IP. TLS ServerName and Host remain the
		// original hostname; the transport cannot perform an unchecked DNS lookup.
		return dial(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
	}
}
func newFetchClient() *http.Client {
	d := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil // Do not bypass destination policy through HTTP_PROXY.
	tr.DialContext = safeDialContext(netLookup, d.DialContext)
	tr.MaxResponseHeaderBytes = 64 * 1024
	tr.ResponseHeaderTimeout = 15 * time.Second
	tr.DisableKeepAlives = true // No implicit transport retry on reused connections.
	return &http.Client{Transport: tr, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		u, err := fetchURL(req.URL.String())
		if err != nil {
			return errBlockedTarget
		}
		if _, err = publicAddresses(req.Context(), u.Hostname(), netLookup); err != nil {
			return err
		}
		return nil
	}}
}
func decodeText(raw []byte, contentType string) (string, error) {
	r, err := charset.NewReader(bytes.NewReader(raw), contentType)
	if err != nil {
		return "", err
	}
	b, err := io.ReadAll(io.LimitReader(r, 4*maxFetchBytes+1))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}
func nodeText(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		if x.Type == html.TextNode {
			b.WriteString(x.Data)
			b.WriteByte(' ')
		}
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.Join(strings.Fields(b.String()), " ")
}
func extractHTML(raw []byte, contentType string, u *url.URL) (string, string, error) {
	text, err := decodeText(raw, contentType)
	if err != nil {
		return "", "", err
	}
	doc, err := html.Parse(strings.NewReader(text))
	if err != nil {
		return "", "", err
	}
	title := ""
	var main, article, body *html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "title":
				title = nodeText(n)
			case "main":
				if main == nil {
					main = n
				}
			case "article":
				if article == nil || len(nodeText(n)) > len(nodeText(article)) {
					article = n
				}
			case "body":
				body = n
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	root := body
	if main != nil {
		root = main
	} else if article != nil {
		root = article
	}
	if root == nil {
		root = doc
	}
	var clean func(*html.Node)
	clean = func(n *html.Node) {
		for c := n.FirstChild; c != nil; {
			next := c.NextSibling
			remove := false
			if c.Type == html.ElementNode {
				switch c.Data {
				case "script", "style", "noscript", "iframe", "object", "embed", "svg", "canvas", "nav", "footer", "aside", "form", "button", "input", "select", "textarea", "head":
					remove = true
				}
				for _, a := range c.Attr {
					if a.Key == "hidden" || a.Key == "aria-hidden" && a.Val == "true" {
						remove = true
					}
				}
			}
			if remove {
				n.RemoveChild(c)
			} else {
				clean(c)
			}
			c = next
		}
	}
	clean(root)
	var b bytes.Buffer
	if err := html.Render(&b, root); err != nil {
		return "", "", err
	}
	conv := converter.NewConverter(converter.WithPlugins(base.NewBasePlugin(), commonmark.NewCommonmarkPlugin(), table.NewTablePlugin()))
	markdown, err := conv.ConvertString(b.String(), converter.WithDomain(u.String()))
	return title, strings.TrimSpace(markdown), err
}
