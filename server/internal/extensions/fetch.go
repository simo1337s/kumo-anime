package extensions

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"

	"github.com/dop251/goja"
	"github.com/imroc/req/v3"
)

// Fetcher performs HTTP requests for extensions. It impersonates Chrome's
// TLS fingerprint (like Seanime) and refuses to connect to loopback or
// private addresses, so an extension can never reach the user's LAN
// (router, torrent client, Kumo itself...).
type Fetcher struct {
	follow   *req.Client
	noFollow *req.Client
	plain    *req.Client
	// AllowedDomains restricts plugin requests (nil = any public host).
	AllowedDomains []string
}

var errPrivateAddress = errors.New("extensions are not allowed to connect to local/private network addresses")

func isPrivateIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() || ip.IsMulticast() || ip.Equal(net.IPv4bcast) ||
		(ip.To4() != nil && ip.To4()[0] == 100 && ip.To4()[1]&0xc0 == 64) // CGNAT 100.64/10
}

func guardedDialer() *net.Dialer {
	return &net.Dialer{
		Timeout: 15 * time.Second,
		Control: func(network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			if ip := net.ParseIP(host); ip != nil && isPrivateIP(ip) {
				return errPrivateAddress
			}
			return nil
		},
	}
}

func NewFetcher() *Fetcher {
	mk := func(impersonate bool, redirects bool) *req.Client {
		c := req.C().SetTimeout(35 * time.Second)
		if impersonate {
			c = c.ImpersonateChrome()
		} else {
			c = c.SetUserAgent("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36")
		}
		d := guardedDialer()
		c.SetDial(d.DialContext)
		if !redirects {
			c.SetRedirectPolicy(req.NoRedirectPolicy())
		}
		return c
	}
	return &Fetcher{follow: mk(true, true), noFollow: mk(true, false), plain: mk(false, true)}
}

// WithDomains returns a copy restricted to the given domains.
func (f *Fetcher) WithDomains(domains []string) *Fetcher {
	c := *f
	c.AllowedDomains = domains
	return &c
}

// Plugin network allowlist that Seanime grants every plugin.
var builtinPluginDomains = []string{
	"api.github.com", "raw.githubusercontent.com", "shikimori.one", "anilist.co", "graphql.anilist.co",
	"myanimelist.net", "*.myanimelist.net", "seanime.app", "trakt.tv", "*.trakt.tv", "kitsu.io",
	"api.simkl.com", "simkl.com", "*.gstatic.com", "*.googleapis.com",
}

func domainAllowed(u *url.URL, rules []string) bool {
	if rules == nil {
		return true
	}
	host := strings.ToLower(u.Hostname())
	for _, rule := range rules {
		rule = strings.ToLower(strings.TrimSpace(rule))
		if rule == "*" {
			return true
		}
		path := ""
		if strings.Contains(rule, "://") {
			if ru, err := url.Parse(rule); err == nil {
				rule, path = ru.Hostname(), ru.Path
			}
		} else if i := strings.IndexByte(rule, '/'); i >= 0 {
			rule, path = rule[:i], rule[i:]
		}
		if h, _, err := net.SplitHostPort(rule); err == nil {
			rule = h
		}
		match := false
		if strings.HasPrefix(rule, "*.") {
			base := rule[2:]
			match = host == base || strings.HasSuffix(host, "."+base)
		} else {
			match = host == rule
		}
		if !match {
			continue
		}
		if path == "" || path == "/" {
			return true
		}
		if strings.HasSuffix(path, "/") && strings.HasPrefix(u.Path, path) || u.Path == path {
			return true
		}
	}
	return false
}

type fetchRequest struct {
	url      string
	method   string
	headers  map[string]string
	body     []byte
	ctype    string
	redirect string
	timeout  time.Duration
	noBypass bool
}

type fetchResponse struct {
	status     int
	statusText string
	method     string
	url        string
	headers    http.Header
	cookies    map[string]string
	redirected bool
	body       []byte
}

func (f *Fetcher) Do(ctx context.Context, fr fetchRequest) (*fetchResponse, error) {
	u, err := url.Parse(fr.url)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("invalid URL %q", fr.url)
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil && isPrivateIP(ip) {
		return nil, errPrivateAddress
	}
	if strings.EqualFold(u.Hostname(), "localhost") {
		return nil, errPrivateAddress
	}
	if !domainAllowed(u, f.AllowedDomains) {
		return nil, fmt.Errorf("network access to %s is not allowed for this plugin", u.Hostname())
	}
	client := f.follow
	if fr.redirect == "manual" || fr.redirect == "error" {
		client = f.noFollow
	} else if fr.noBypass {
		client = f.plain
	}
	timeout := fr.timeout
	if timeout <= 0 {
		timeout = 35 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	r := client.R().SetContext(ctx)
	for k, v := range fr.headers {
		r.SetHeader(k, v)
	}
	if fr.body != nil {
		r.SetBodyBytes(fr.body)
		if fr.ctype != "" {
			r.SetHeader("Content-Type", fr.ctype)
		}
	}
	method := strings.ToUpper(fr.method)
	if method == "" {
		method = http.MethodGet
	}
	resp, err := r.Send(method, fr.url)
	if err != nil {
		return nil, err
	}
	if resp.Response == nil {
		return nil, errors.New("no response")
	}
	defer resp.Body.Close()
	if fr.redirect == "error" && resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return nil, errors.New("redirect not allowed")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, err
	}
	out := &fetchResponse{
		status:     resp.StatusCode,
		statusText: http.StatusText(resp.StatusCode),
		method:     method,
		url:        resp.Request.URL.String(),
		headers:    resp.Header,
		cookies:    map[string]string{},
		body:       body,
	}
	out.redirected = out.url != fr.url
	for _, c := range resp.Cookies() {
		out.cookies[c.Name] = c.Value
	}
	return out, nil
}

// fetch implements the global fetch() binding (always returns a Promise).
func (r *Runtime) fetch(vm *goja.Runtime, call goja.FunctionCall) goja.Value {
	promise, resolve, reject := vm.NewPromise()
	fr, err := parseFetchArgs(vm, call)
	if err != nil {
		_ = reject(vm.NewGoError(err))
		return vm.ToValue(promise)
	}
	f := r.fetcher
	if f == nil {
		f = NewFetcher()
		r.fetcher = f
	}
	go func() {
		resp, err := f.Do(context.Background(), fr)
		r.loop.RunOnLoop(func(vm *goja.Runtime) {
			if err != nil {
				r.log("warn", "fetch "+fr.url+": "+err.Error())
				_ = reject(vm.NewGoError(err))
				return
			}
			_ = resolve(responseObject(vm, resp))
		})
	}()
	return vm.ToValue(promise)
}

func parseFetchArgs(vm *goja.Runtime, call goja.FunctionCall) (fetchRequest, error) {
	fr := fetchRequest{url: call.Argument(0).String(), headers: map[string]string{}}
	opts := call.Argument(1)
	if opts == nil || goja.IsUndefined(opts) || goja.IsNull(opts) {
		return fr, nil
	}
	o := opts.ToObject(vm)
	if m := o.Get("method"); m != nil && !goja.IsUndefined(m) {
		fr.method = m.String()
	}
	if h := o.Get("headers"); h != nil && !goja.IsUndefined(h) && !goja.IsNull(h) {
		ho := h.ToObject(vm)
		for _, k := range ho.Keys() {
			v := ho.Get(k)
			if _, ok := v.Export().(string); ok {
				fr.headers[k] = v.String()
			} else if v != nil && !goja.IsUndefined(v) && !goja.IsNull(v) {
				fr.headers[k] = v.String()
			}
		}
	}
	if t := o.Get("timeout"); t != nil && !goja.IsUndefined(t) {
		fr.timeout = time.Duration(t.ToInteger()) * time.Second
	}
	if rd := o.Get("redirect"); rd != nil && !goja.IsUndefined(rd) {
		fr.redirect = rd.String()
	}
	if nb := o.Get("noCloudflareBypass"); nb != nil && !goja.IsUndefined(nb) {
		fr.noBypass = nb.ToBoolean()
	}
	if b := o.Get("body"); b != nil && !goja.IsUndefined(b) && !goja.IsNull(b) {
		switch x := b.Export().(type) {
		case string:
			fr.body = []byte(x)
		case []byte:
			fr.body = x
		case goja.ArrayBuffer:
			fr.body = x.Bytes()
		default:
			bo := b.ToObject(vm)
			if fd := bo.Get("__kumoFormData"); fd != nil && fd.ToBoolean() {
				body, ctype, err := encodeFormData(vm, bo)
				if err != nil {
					return fr, err
				}
				fr.body, fr.ctype = body, ctype
			} else if bo.Get("byteLength") != nil && !goja.IsUndefined(bo.Get("byteLength")) {
				fr.body = valueToBytes(vm, b)
			} else if bo.ClassName() == "Object" || bo.ClassName() == "Array" {
				js, _ := goja.AssertFunction(vm.Get("JSON").ToObject(vm).Get("stringify"))
				s, err := js(goja.Undefined(), b)
				if err != nil {
					return fr, err
				}
				fr.body = []byte(s.String())
				if !hasHeader(fr.headers, "Content-Type") {
					fr.ctype = "application/json"
				}
			} else {
				fr.body = []byte(b.String())
			}
		}
	}
	return fr, nil
}

func hasHeader(h map[string]string, name string) bool {
	for k := range h {
		if strings.EqualFold(k, name) {
			return true
		}
	}
	return false
}

func encodeFormData(vm *goja.Runtime, fd *goja.Object) ([]byte, string, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	entries := fd.Get("_entries").ToObject(vm)
	n := int(entries.Get("length").ToInteger())
	for i := 0; i < n; i++ {
		e := entries.Get(fmt.Sprint(i)).ToObject(vm)
		k := e.Get("0").String()
		v := e.Get("1")
		if _, ok := v.Export().(string); ok || goja.IsUndefined(v) {
			_ = w.WriteField(k, v.String())
			continue
		}
		part, err := w.CreateFormFile(k, k)
		if err != nil {
			return nil, "", err
		}
		_, _ = part.Write(valueToBytes(vm, v))
	}
	if err := w.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), w.FormDataContentType(), nil
}

func responseObject(vm *goja.Runtime, resp *fetchResponse) goja.Value {
	o := vm.NewObject()
	_ = o.Set("status", resp.status)
	_ = o.Set("statusText", resp.statusText)
	_ = o.Set("method", resp.method)
	_ = o.Set("ok", resp.status >= 200 && resp.status < 300)
	_ = o.Set("url", resp.url)
	_ = o.Set("redirected", resp.redirected)
	headers := map[string]string{}
	raw := map[string][]string{}
	for k, v := range resp.headers {
		raw[k] = v
		if len(v) > 0 {
			headers[k] = v[0]
		}
	}
	_ = o.Set("headers", toJS(vm, headers))
	_ = o.Set("rawHeaders", toJS(vm, raw))
	_ = o.Set("cookies", toJS(vm, resp.cookies))
	_ = o.Set("contentType", resp.headers.Get("Content-Type"))
	_ = o.Set("contentLength", len(resp.body))
	body := resp.body
	_ = o.Set("body", bytesToJS(vm, body))
	_ = o.Set("text", func() string { return string(body) })
	_ = o.Set("json", func() goja.Value {
		parse, _ := goja.AssertFunction(vm.Get("JSON").ToObject(vm).Get("parse"))
		v, err := parse(goja.Undefined(), vm.ToValue(string(body)))
		if err != nil {
			return goja.Null()
		}
		return v
	})
	_ = o.Set("arrayBuffer", func() goja.Value { return vm.ToValue(vm.NewArrayBuffer(append([]byte(nil), body...))) })
	return o
}
