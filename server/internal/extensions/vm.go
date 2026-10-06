package extensions

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dop251/goja"
	"github.com/dop251/goja_nodejs/buffer"
	"github.com/dop251/goja_nodejs/eventloop"
	"github.com/dop251/goja_nodejs/require"
	"github.com/dop251/goja_nodejs/url"
	"github.com/evanw/esbuild/pkg/api"

	"github.com/5rahim/habari"

	"github.com/simo1337s/animetest/server/internal/util"
)

// Compile turns a payload into goja-ready JavaScript.
func Compile(m *Manifest, payload string) (*goja.Program, error) {
	src := payload
	if strings.EqualFold(m.Language, "typescript") {
		res := api.Transform(payload, api.TransformOptions{
			Target:           api.ES2018,
			Loader:           api.LoaderTS,
			Format:           api.FormatDefault,
			MinifyWhitespace: true,
			MinifySyntax:     true,
			Sourcemap:        api.SourceMapNone,
		})
		if len(res.Errors) > 0 {
			e := res.Errors[0]
			loc := ""
			if e.Location != nil {
				loc = fmt.Sprintf(" (line %d)", e.Location.Line)
			}
			return nil, fmt.Errorf("typescript: %s%s", e.Text, loc)
		}
		src = string(res.Code)
	}
	return goja.Compile(m.ID+".js", src, false)
}

// ApplyUserConfig replaces {{field}} placeholders in the payload and returns
// the effective values for $getUserPreference.
func ApplyUserConfig(m *Manifest, payload string, saved *SavedUserConfig) (string, map[string]string, error) {
	values := map[string]string{}
	if m.UserConfig == nil {
		return payload, values, nil
	}
	var err error
	if saved == nil || saved.Values == nil {
		if m.UserConfig.RequiresConfig {
			err = errors.New("this extension needs to be configured")
		}
		saved = &SavedUserConfig{Values: map[string]string{}}
	} else if saved.Version != m.UserConfig.Version && m.UserConfig.RequiresConfig {
		err = errors.New("the extension settings changed, please configure it again")
	}
	for _, f := range m.UserConfig.Fields {
		v, ok := saved.Values[f.Name]
		if !ok || v == "" {
			v = f.Default
		}
		if v != "" {
			values[f.Name] = v
		}
		if err == nil || v != "" {
			payload = strings.ReplaceAll(payload, "{{"+f.Name+"}}", v)
		}
	}
	return payload, values, err
}

// LogLine is a console message produced by an extension.
type LogLine struct {
	Time    int64  `json:"time"`
	Level   string `json:"level"`
	Message string `json:"message"`
}

// Runtime is one JavaScript VM running an extension on its own event loop.
type Runtime struct {
	ID       string
	manifest *Manifest
	loop     *eventloop.EventLoop
	// vm is the loop's VM, published by the loop's first job so that Close
	// can interrupt it. Off the loop goroutine only vm.Interrupt may be used.
	vm atomic.Pointer[goja.Runtime]

	// ctx is cancelled by Close. Blocking host calls ($sleep, plugin
	// services, fetch) watch it so they can't keep the loop busy.
	ctx    context.Context
	cancel context.CancelFunc
	closed atomic.Bool

	prefs   map[string]string
	store   *MemStore
	storage *Storage
	fetcher *Fetcher

	logMu sync.Mutex
	logs  []LogLine
}

type RuntimeOptions struct {
	Prefs   map[string]string
	Store   *MemStore
	Storage *Storage
	Fetcher *Fetcher
	// Extra lets callers install additional globals before the payload runs.
	Extra func(r *Runtime, vm *goja.Runtime) error
	// LoadTimeout bounds running the payload's top-level code
	// (defaultLoadTimeout when zero).
	LoadTimeout time.Duration
	// Context aborts loading when cancelled. It has no effect once
	// NewRuntime has returned.
	Context context.Context
}

const (
	defaultLoadTimeout = 30 * time.Second
	// closeTimeout bounds how long Close waits for the event loop to stop
	// (only reached when the loop is stuck in a Go call that ignores ctx).
	closeTimeout = 5 * time.Second
)

// errStopped is returned to callers waiting on a runtime that was closed.
var errStopped = errors.New("the extension was stopped")

// noFileModules makes require() unable to load files: extensions are single
// payloads, and the default loader would let them read any file on disk
// (require("/path/to/file.json") returns its parsed content). Built-in
// modules (buffer, url...) still load.
func noFileModules(string) ([]byte, error) { return nil, require.ModuleFileDoesNotExistError }

// NewRuntime starts an event loop, installs the bindings and runs the
// program.
func NewRuntime(m *Manifest, prog *goja.Program, opts RuntimeOptions) (*Runtime, error) {
	reg := require.NewRegistry(require.WithLoader(noFileModules))
	loop := eventloop.NewEventLoop(eventloop.WithRegistry(reg), eventloop.EnableConsole(false))
	ctx, cancel := context.WithCancel(context.Background())
	r := &Runtime{ID: m.ID, manifest: m, loop: loop, ctx: ctx, cancel: cancel,
		prefs: opts.Prefs, store: opts.Store, storage: opts.Storage, fetcher: opts.Fetcher}
	if r.store == nil {
		r.store = NewMemStore()
	}
	if r.prefs == nil {
		r.prefs = map[string]string{}
	}
	loop.Start()

	errCh := make(chan error, 1)
	// The loop's first job publishes the VM before running anything, then
	// checks for a concurrent Close: either Close sees the VM and interrupts
	// it, or this job sees the close and bails out.
	ok := loop.RunOnLoop(func(vm *goja.Runtime) {
		r.vm.Store(vm)
		if r.closed.Load() {
			errCh <- errStopped
			return
		}
		defer func() {
			if p := recover(); p != nil {
				errCh <- fmt.Errorf("panic: %v", p)
			}
		}()
		vm.SetFieldNameMapper(goja.TagFieldNameMapper("json", true))
		buffer.Enable(vm)
		url.Enable(vm)
		if err := r.installBindings(vm); err != nil {
			errCh <- err
			return
		}
		if opts.Extra != nil {
			if err := opts.Extra(r, vm); err != nil {
				errCh <- err
				return
			}
		}
		_, err := vm.RunProgram(prog)
		errCh <- jsError(err)
	})
	if !ok {
		r.Close()
		return nil, errors.New("event loop not running")
	}
	timeout := opts.LoadTimeout
	if timeout <= 0 {
		timeout = defaultLoadTimeout
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	var abort <-chan struct{}
	if opts.Context != nil {
		abort = opts.Context.Done()
	}
	select {
	case err := <-errCh:
		if err != nil {
			r.Close()
			return nil, err
		}
	case <-timer.C:
		r.Close()
		return nil, errors.New("extension took too long to load")
	case <-abort:
		r.Close()
		return nil, errStopped
	}
	return r, nil
}

// Close stops the runtime: blocking host calls are cancelled, running
// JavaScript is interrupted (so an endless loop in top-level code or in a
// callback can't keep it alive) and queued callbacks are dropped. It never
// blocks for more than closeTimeout, and is safe to call from any goroutine
// and more than once.
func (r *Runtime) Close() {
	if !r.closed.CompareAndSwap(false, true) {
		return
	}
	r.cancel()
	terminated := make(chan struct{})
	go func() {
		defer close(terminated)
		r.loop.Terminate()
	}()
	r.interrupt()
	// goja clears an interrupt once it has unwound the JS stack, and
	// Terminate itself runs callbacks that were already queued (e.g.
	// setImmediate), so keep interrupting until the loop is gone.
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	deadline := time.NewTimer(closeTimeout)
	defer deadline.Stop()
	for waiting := true; waiting; {
		select {
		case <-terminated:
			waiting = false
		case <-tick.C:
			r.interrupt()
		case <-deadline.C:
			log.Printf("[ext:%s] the extension did not stop within %s", r.ID, closeTimeout)
			waiting = false
		}
	}
	r.store.Unwatch(r)
}

func (r *Runtime) interrupt() {
	if vm := r.vm.Load(); vm != nil {
		vm.Interrupt(errStopped)
	}
}

func (r *Runtime) Logs() []LogLine {
	r.logMu.Lock()
	defer r.logMu.Unlock()
	return append([]LogLine(nil), r.logs...)
}

func (r *Runtime) log(level, msg string) {
	r.logMu.Lock()
	r.logs = append(r.logs, LogLine{Time: time.Now().Unix(), Level: level, Message: msg})
	if len(r.logs) > 300 {
		r.logs = r.logs[len(r.logs)-300:]
	}
	r.logMu.Unlock()
	if level == "error" || level == "warn" {
		log.Printf("[ext:%s] %s: %s", r.ID, level, truncate(msg, 400))
	}
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

// RunOnLoop schedules fn on the VM goroutine. It returns false once the
// runtime is closed, and a job still queued at that point is dropped. A
// panic in fn is logged: the event loop goroutine has no recover, so it
// would otherwise take the whole process down.
func (r *Runtime) RunOnLoop(fn func(vm *goja.Runtime)) bool {
	if r.closed.Load() {
		return false
	}
	return r.loop.RunOnLoop(func(vm *goja.Runtime) {
		if r.closed.Load() {
			return
		}
		defer func() {
			if p := recover(); p != nil && !r.closed.Load() {
				r.log("error", fmt.Sprintf("unexpected error: %v", p))
			}
		}()
		fn(vm)
	})
}

func jsError(err error) error {
	if err == nil {
		return nil
	}
	var ex *goja.Exception
	if errors.As(err, &ex) {
		msg := ex.Value().String()
		if obj, ok := ex.Value().(*goja.Object); ok {
			if m := obj.Get("message"); m != nil && !goja.IsUndefined(m) {
				msg = m.String()
			}
		}
		return errors.New(msg)
	}
	return err
}

// ---------------------------------------------------------------------------
// Calling into JS

// awaitValue resolves v (which may be a Promise) and delivers the result as
// JSON to done. Must run on the loop.
func (r *Runtime) awaitValue(vm *goja.Runtime, v goja.Value, done func(raw json.RawMessage, err error)) {
	stringify := func(val goja.Value) (json.RawMessage, error) {
		if val == nil || goja.IsUndefined(val) || goja.IsNull(val) {
			return json.RawMessage("null"), nil
		}
		js, ok := jsonFunc(vm, "stringify")
		if !ok {
			return nil, errors.New("JSON.stringify is not available")
		}
		s, err := js(goja.Undefined(), val)
		if err != nil {
			return nil, jsError(err)
		}
		if goja.IsUndefined(s) {
			return json.RawMessage("null"), nil
		}
		return json.RawMessage(s.String()), nil
	}
	if p, ok := v.Export().(*goja.Promise); ok {
		switch p.State() {
		case goja.PromiseStateFulfilled:
			done(stringify(p.Result()))
			return
		case goja.PromiseStateRejected:
			done(nil, rejectionError(p.Result()))
			return
		}
		obj := v.ToObject(vm)
		then, ok := goja.AssertFunction(obj.Get("then"))
		if !ok {
			done(nil, errors.New("the returned promise has no then()"))
			return
		}
		onOk := vm.ToValue(func(call goja.FunctionCall) goja.Value {
			done(stringify(call.Argument(0)))
			return goja.Undefined()
		})
		onErr := vm.ToValue(func(call goja.FunctionCall) goja.Value {
			done(nil, rejectionError(call.Argument(0)))
			return goja.Undefined()
		})
		if _, err := then(obj, onOk, onErr); err != nil {
			done(nil, jsError(err))
		}
		return
	}
	done(stringify(v))
}

func rejectionError(v goja.Value) error {
	if v == nil || goja.IsUndefined(v) {
		return errors.New("promise rejected")
	}
	if obj, ok := v.(*goja.Object); ok {
		if m := obj.Get("message"); m != nil && !goja.IsUndefined(m) {
			return errors.New(m.String())
		}
	}
	return errors.New(v.String())
}

// toJS converts a Go value into a plain JS object via JSON.parse.
func toJS(vm *goja.Runtime, v any) goja.Value {
	raw, err := json.Marshal(v)
	if err != nil {
		return goja.Undefined()
	}
	return parseJS(vm, raw)
}

// jsonFunc returns JSON.parse or JSON.stringify. Extension code can delete
// or replace the JSON global, so this must not assume it exists: calling a
// method on a nil goja.Value panics with a Go runtime error, which goja
// doesn't turn into a JS exception and which would crash the process.
func jsonFunc(vm *goja.Runtime, name string) (goja.Callable, bool) {
	j, ok := vm.Get("JSON").(*goja.Object)
	if !ok {
		return nil, false
	}
	return goja.AssertFunction(j.Get(name))
}

// intProp reads an integer property, treating a missing one as 0.
func intProp(o *goja.Object, name string) int64 {
	if v := o.Get(name); v != nil {
		return v.ToInteger()
	}
	return 0
}

// CallProvider instantiates the global Provider class and calls a method,
// awaiting Promises. The result is returned as JSON.
func (r *Runtime) CallProvider(ctx context.Context, method string, args ...any) (json.RawMessage, error) {
	type result struct {
		raw json.RawMessage
		err error
	}
	ch := make(chan result, 1)
	var once sync.Once
	send := func(raw json.RawMessage, err error) { once.Do(func() { ch <- result{raw, err} }) }
	ok := r.RunOnLoop(func(vm *goja.Runtime) {
		defer func() {
			if p := recover(); p != nil {
				send(nil, fmt.Errorf("panic: %v", p))
			}
		}()
		ctor := vm.Get("Provider")
		if ctor == nil || goja.IsUndefined(ctor) {
			send(nil, errors.New("extension does not define a Provider class"))
			return
		}
		inst, err := vm.New(ctor)
		if err != nil {
			send(nil, jsError(err))
			return
		}
		fn, isFn := goja.AssertFunction(inst.Get(method))
		if !isFn {
			send(nil, fmt.Errorf("provider has no %s() method", method))
			return
		}
		jsArgs := make([]goja.Value, len(args))
		for i, a := range args {
			if s, ok := a.(string); ok {
				jsArgs[i] = vm.ToValue(s)
			} else {
				jsArgs[i] = toJS(vm, a)
			}
		}
		v, err := fn(inst, jsArgs...)
		if err != nil {
			send(nil, jsError(err))
			return
		}
		r.awaitValue(vm, v, send)
	})
	if !ok {
		return nil, errors.New("extension is not running")
	}
	timeout := time.NewTimer(90 * time.Second)
	defer timeout.Stop()
	select {
	case res := <-ch:
		if res.err != nil {
			r.log("error", method+": "+res.err.Error())
		}
		return res.raw, res.err
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-r.ctx.Done():
		return nil, errStopped
	case <-timeout.C:
		return nil, fmt.Errorf("%s() timed out", method)
	}
}

// HasMethod reports whether Provider.prototype has the method.
func (r *Runtime) HasMethod(method string) bool {
	ch := make(chan bool, 1)
	ok := r.RunOnLoop(func(vm *goja.Runtime) {
		has := false
		defer func() { ch <- has }()
		// Provider may be anything (null, a primitive, a Proxy...).
		ctor, isObj := vm.Get("Provider").(*goja.Object)
		if !isObj {
			return
		}
		proto, isObj := ctor.Get("prototype").(*goja.Object)
		if !isObj {
			return
		}
		_, has = goja.AssertFunction(proto.Get(method))
	})
	if !ok {
		return false
	}
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()
	select {
	case v := <-ch:
		return v
	case <-r.ctx.Done():
		return false
	case <-timeout.C:
		return false
	}
}

// ---------------------------------------------------------------------------
// Global bindings shared by providers and plugins

func (r *Runtime) installBindings(vm *goja.Runtime) error {
	// console
	console := vm.NewObject()
	for _, lvl := range []string{"log", "info", "warn", "error", "debug"} {
		level := lvl
		_ = console.Set(level, func(call goja.FunctionCall) goja.Value {
			parts := make([]string, 0, len(call.Arguments))
			for _, a := range call.Arguments {
				parts = append(parts, stringifyForLog(vm, a))
			}
			l := level
			if l == "log" || l == "debug" {
				l = "info"
			}
			r.log(l, strings.Join(parts, " "))
			return goja.Undefined()
		})
	}
	_ = vm.Set("console", console)

	_ = vm.Set("fetch", func(call goja.FunctionCall) goja.Value { return r.fetch(vm, call) })

	_ = vm.Set("$getUserPreference", func(key string) goja.Value {
		if v, ok := r.prefs[key]; ok && v != "" {
			return vm.ToValue(v)
		}
		return goja.Undefined()
	})
	_ = vm.Set("$toString", func(v goja.Value) string { return valueToString(vm, v) })
	_ = vm.Set("$toBytes", func(v goja.Value) goja.Value { return bytesToJS(vm, valueToBytes(vm, v)) })
	// $sleep blocks the loop, so it must give up when the runtime is closed.
	_ = vm.Set("$sleep", func(ms int64) {
		if ms <= 0 {
			return
		}
		t := time.NewTimer(time.Duration(ms) * time.Millisecond)
		defer t.Stop()
		select {
		case <-t.C:
		case <-r.ctx.Done():
		}
	})
	_ = vm.Set("$isOffline", func() bool { return false })
	_ = vm.Set("$toError", func(v string) error { return errors.New(v) })
	_ = vm.Set("atob", func(s string) (string, error) {
		s = strings.TrimRight(strings.TrimSpace(s), "=")
		b, err := base64.RawStdEncoding.DecodeString(s)
		if err != nil {
			return "", err
		}
		// atob returns a "binary string" (one char per byte)
		rs := make([]rune, len(b))
		for i, c := range b {
			rs[i] = rune(c)
		}
		return string(rs), nil
	})
	_ = vm.Set("btoa", func(s string) string {
		b := make([]byte, 0, len(s))
		for _, c := range s {
			b = append(b, byte(c))
		}
		return base64.StdEncoding.EncodeToString(b)
	})
	_ = vm.Set("$habari", map[string]any{
		"parse": func(name string) goja.Value { return toJS(vm, habari.Parse(name)) },
	})
	r.installScannerUtils(vm)
	_ = vm.Set("$store", r.store.Bind(r, vm))
	if r.storage != nil {
		_ = vm.Set("$storage", r.storage.Bind(vm))
	}
	installDoc(vm)
	installCrypto(vm)

	// JS helpers that are simpler to write in JS.
	_, err := vm.RunString(jsPrelude)
	return err
}

func stringifyForLog(vm *goja.Runtime, v goja.Value) string {
	if v == nil || goja.IsUndefined(v) {
		return "undefined"
	}
	if obj, ok := v.(*goja.Object); ok {
		// Read each property once: a getter can delete it after the first read.
		if st := obj.Get("stack"); st != nil && !goja.IsUndefined(st) {
			if msg := obj.Get("message"); msg != nil {
				return msg.String() + "\n" + st.String()
			}
		}
		if js, ok := jsonFunc(vm, "stringify"); ok {
			if s, err := js(goja.Undefined(), v); err == nil && !goja.IsUndefined(s) {
				return s.String()
			}
		}
	}
	return v.String()
}

func valueToBytes(vm *goja.Runtime, v goja.Value) []byte {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return nil
	}
	switch x := v.Export().(type) {
	case []byte:
		return x
	case string:
		return []byte(x)
	case goja.ArrayBuffer:
		return x.Bytes()
	}
	if obj, ok := v.(*goja.Object); ok {
		// Typed arrays / Buffer: read through .buffer + byteOffset/length.
		// Any object can have a "buffer" property, so the offsets are
		// untrusted.
		if buf := obj.Get("buffer"); buf != nil {
			if ab, ok := buf.Export().(goja.ArrayBuffer); ok {
				off, n := intProp(obj, "byteOffset"), intProp(obj, "byteLength")
				b := ab.Bytes()
				if off >= 0 && n >= 0 && off <= int64(len(b)) && n <= int64(len(b))-off {
					return append([]byte(nil), b[off:off+n]...)
				}
			}
		}
		if arr, ok := obj.Export().([]any); ok {
			out := make([]byte, len(arr))
			for i, e := range arr {
				switch n := e.(type) {
				case int64:
					out[i] = byte(n)
				case float64:
					out[i] = byte(n)
				}
			}
			return out
		}
		raw, _ := json.Marshal(obj.Export())
		return raw
	}
	return []byte(v.String())
}

func valueToString(vm *goja.Runtime, v goja.Value) string {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return ""
	}
	switch x := v.Export().(type) {
	case string:
		return x
	case []byte:
		return string(x)
	case goja.ArrayBuffer:
		return string(x.Bytes())
	}
	if obj, ok := v.(*goja.Object); ok {
		if b := valueToBytes(vm, obj); b != nil {
			if obj.Get("byteLength") != nil && !goja.IsUndefined(obj.Get("byteLength")) {
				return string(b)
			}
		}
		if js, ok := jsonFunc(vm, "stringify"); ok {
			if s, err := js(goja.Undefined(), v); err == nil {
				return s.String()
			}
		}
	}
	return v.String()
}

// bytesToJS returns a Uint8Array.
func bytesToJS(vm *goja.Runtime, b []byte) goja.Value {
	ab := vm.NewArrayBuffer(append([]byte(nil), b...))
	u8, err := vm.New(vm.Get("Uint8Array"), vm.ToValue(ab))
	if err != nil {
		return vm.ToValue(ab)
	}
	return u8
}

func (r *Runtime) installScannerUtils(vm *goja.Runtime) {
	normalize := func(t string) map[string]any {
		n := util.NormalizeTitle(t)
		return map[string]any{
			"original": t, "normalized": n, "cleanBaseTitle": n, "denoisedTitle": n,
			"tokens": strings.Fields(n), "season": extractNum(reSeason, t), "part": extractNum(rePart, t),
			"year": extractNum(reYear, t), "isMain": extractNum(reSeason, t) <= 1,
		}
	}
	_ = vm.Set("$scannerUtils", map[string]any{
		"normalizeTitle":      func(t string) goja.Value { return toJS(vm, normalize(t)) },
		"compareTitles":       func(a, b string) float64 { return util.Similarity(a, b) },
		"extractSeasonNumber": func(t string) int { return extractNum(reSeason, t) },
		"extractPartNumber":   func(t string) int { return extractNum(rePart, t) },
		"extractYear":         func(t string) int { return extractNum(reYear, t) },
		"findBestMatch": func(target string, cands []string) string {
			best, score := "", -1.0
			for _, c := range cands {
				if s := util.Similarity(target, c); s > score {
					best, score = c, s
				}
			}
			return best
		},
		"getSignificantTokens": func(t string) []string { return strings.Fields(util.NormalizeTitle(t)) },
		"buildSearchQuery":     func(t string) string { return util.NormalizeTitle(t) },
		"sanitizeQuery": func(q string) string {
			return strings.Join(strings.Fields(strings.NewReplacer("|", " ", "(", " ", ")", " ", "\"", " ", "-", " ").Replace(q)), " ")
		},
		"buildAdvancedQuery": func(ts []string) string {
			if len(ts) == 1 {
				return ts[0]
			}
			return "(" + strings.Join(ts, " | ") + ")"
		},
		"buildSeasonQuery": func(t string, n int) string {
			if n <= 1 {
				return t
			}
			return fmt.Sprintf("(%s S%02d | %s Season %d | %s %s Season)", t, n, t, n, t, ordinalStr(n))
		},
		"buildPartQuery": func(t string, n int) string {
			if n <= 1 {
				return t
			}
			return fmt.Sprintf("(%s Part %d | %s %s Cour)", t, n, t, ordinalStr(n))
		},
		"buildSmartSearchTitles": func(ts []string) goja.Value {
			seen := map[string]bool{}
			out := []string{}
			season, part := -1, -1
			for _, t := range ts {
				if s := extractNum(reSeason, t); s > 0 && season < 0 {
					season = s
				}
				if p := extractNum(rePart, t); p > 0 && part < 0 {
					part = p
				}
				c := strings.TrimSpace(t)
				if c != "" && !seen[strings.ToLower(c)] {
					seen[strings.ToLower(c)] = true
					out = append(out, c)
				}
			}
			return toJS(vm, map[string]any{"titles": out, "season": season, "part": part})
		},
	})
}
