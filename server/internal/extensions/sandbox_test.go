package extensions

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dop251/goja"

	"github.com/simo1337s/animetest/server/internal/config"
	"github.com/simo1337s/animetest/server/internal/db"
	"github.com/simo1337s/animetest/server/internal/events"
)

// ---------------------------------------------------------------------------
// Helpers

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "kumo.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	st, err := config.NewStore(d)
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager(d, st, events.NewHub())
	m.loadTimeout = 5 * time.Second
	t.Cleanup(m.Shutdown) // runs before the database is closed
	return m
}

// installTestExtension registers an extension as if it had been installed
// (and, for plugins, granted).
func installTestExtension(t *testing.T, m *Manager, man *Manifest, payload string, grant bool) *Loaded {
	t.Helper()
	l := &Loaded{mgr: m, Manifest: man, payload: payload, Enabled: true}
	if err := m.persist(l); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.exts[man.ID] = l
	m.mu.Unlock()
	if grant {
		if err := m.db.SetKV("ext_grant:"+man.ID, permissionHash(man)); err != nil {
			t.Fatal(err)
		}
	}
	return l
}

func pluginManifest(id string, scopes ...string) *Manifest {
	return &Manifest{ID: id, Name: id, Version: "1.0.0", Type: TypePlugin, Language: "javascript",
		Plugin: &PluginManifest{Version: "1", Permissions: PluginPermissions{Scopes: scopes}}}
}

func providerManifest(id string) *Manifest {
	return &Manifest{ID: id, Name: id, Version: "1.0.0", Type: TypeOnlineStream, Language: "javascript"}
}

func testRuntime(t *testing.T, src string, opts RuntimeOptions) *Runtime {
	t.Helper()
	man := providerManifest("test-provider")
	prog, err := Compile(man, src)
	if err != nil {
		t.Fatal(err)
	}
	rt, err := NewRuntime(man, prog, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(rt.Close)
	return rt
}

func storeRaw(s *MemStore, key string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, ok := s.data[key]
	return string(raw), ok
}

func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func returnsWithin(t *testing.T, timeout time.Duration, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		t.Fatalf("%s did not return within %s", what, timeout)
	}
}

// hostRecorder stubs the app services and records the calls plugins make.
type hostRecorder struct {
	mu    sync.Mutex
	calls []string
}

func (r *hostRecorder) record(format string, args ...any) {
	r.mu.Lock()
	r.calls = append(r.calls, fmt.Sprintf(format, args...))
	r.mu.Unlock()
}

func (r *hostRecorder) Calls() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

func (r *hostRecorder) services() PluginHostServices {
	return PluginHostServices{
		Collection: func(ctx context.Context, mediaType string, refresh bool) (any, error) {
			r.record("collection:%s", mediaType)
			return map[string]any{"MediaListCollection": map[string]any{"lists": []any{}}}, nil
		},
		UpdateEntry: func(ctx context.Context, id int, status *string, score *float64, progress *int) error {
			p := -1
			if progress != nil {
				p = *progress
			}
			r.record("updateEntry:%d:%d", id, p)
			return nil
		},
		DeleteEntry: func(ctx context.Context, id int) error {
			r.record("deleteEntry:%d", id)
			return nil
		},
		CustomQuery: func(ctx context.Context, body map[string]any, token string) (any, error) {
			r.record("customQuery:token=%s", token)
			return map[string]any{}, nil
		},
		LocalFiles: func() (any, error) {
			r.record("localFiles")
			return []any{}, nil
		},
		Token:  func() string { return "user-token" },
		Viewer: func() (string, string) { return "user", "avatar.png" },
	}
}

// liveHeartbeats counts the runtimes still bumping a "beat:<id>" key.
func liveHeartbeats(s *MemStore) int {
	snap := func() map[string]string {
		s.mu.Lock()
		defer s.mu.Unlock()
		out := map[string]string{}
		for k, v := range s.data {
			if strings.HasPrefix(k, "beat:") {
				out[k] = string(v)
			}
		}
		return out
	}
	before := snap()
	time.Sleep(100 * time.Millisecond)
	alive := 0
	for k, v := range snap() {
		if before[k] != v {
			alive++
		}
	}
	return alive
}

// ---------------------------------------------------------------------------
// Plugin permissions

func TestPluginWithoutScopesCannotReachHost(t *testing.T) {
	m := newTestManager(t)
	rec := &hostRecorder{}
	m.Host = rec.services()
	l := installTestExtension(t, m, pluginManifest("noscope"), `
var report = {
  topHost: typeof __host,
  topGlobalHost: typeof globalThis.__host,
  topAnilist: typeof $anilist,
  topDatabase: typeof $database,
  topEntryPoint: typeof __kumoHook
};
function init() {
  $ui.register(function(ctx) {
    report.uiHost = typeof __host;
    report.leakedGlobals = Object.getOwnPropertyNames(globalThis).filter(function(k) {
      return /host|kumo/i.test(k);
    }).join(',');
    // ctx.manga needs no scope (like in Seanime).
    ctx.manga.getCollection().then(function(c) { $store.set('manga', c); },
      function(e) { $store.set('manga', 'error: ' + e.message); });
    $store.set('report', report);
  });
}`, true)
	m.startPlugin(l)

	inf, _ := m.Get("noscope")
	if !inf.Running || inf.Error != "" {
		t.Fatalf("plugin not running: running=%v error=%q", inf.Running, inf.Error)
	}
	raw, ok := storeRaw(m.store("noscope"), "report")
	if !ok {
		t.Fatal("the plugin did not report")
	}
	var report map[string]string
	if err := json.Unmarshal([]byte(raw), &report); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"topHost", "topGlobalHost", "topAnilist", "topDatabase", "topEntryPoint", "uiHost"} {
		if report[k] != "undefined" {
			t.Errorf("%s: typeof = %q, want undefined", k, report[k])
		}
	}
	if report["leakedGlobals"] != "" {
		t.Errorf("host globals visible to plugin code: %s", report["leakedGlobals"])
	}
	if manga, _ := storeRaw(m.store("noscope"), "manga"); !strings.Contains(manga, "MediaListCollection") {
		t.Errorf("ctx.manga.getCollection() = %s", manga)
	}
	for _, c := range rec.Calls() {
		if !strings.HasPrefix(c, "collection:") {
			t.Errorf("unexpected host call without permission: %s", c)
		}
	}
}

func TestPluginWithAnilistScope(t *testing.T) {
	m := newTestManager(t)
	rec := &hostRecorder{}
	m.Host = rec.services()
	l := installTestExtension(t, m, pluginManifest("withscope", "anilist"), `
function init() {
  $ui.register(function(ctx) {
    $store.set('report', { host: typeof __host, anilist: typeof $anilist, database: typeof $database });
    $anilist.updateEntryProgress(42, 7);
  });
}`, true)
	m.startPlugin(l)
	if inf, _ := m.Get("withscope"); inf.Error != "" {
		t.Fatalf("plugin error: %s", inf.Error)
	}
	raw, _ := storeRaw(m.store("withscope"), "report")
	if raw != `{"host":"undefined","anilist":"object","database":"undefined"}` {
		t.Errorf("report = %s", raw)
	}
	if calls := rec.Calls(); !slices.Contains(calls, "updateEntry:42:7") {
		t.Errorf("updateEntry not called: %v", calls)
	}
}

// The Go bindings check scopes themselves, whatever the prelude does.
func TestPluginBindingsCheckScopes(t *testing.T) {
	const src = `
var out = {};
function attempt(name, fn) {
  try { var v = fn(); out[name] = 'ok:' + (typeof v === 'string' ? v : JSON.stringify(v)); }
  catch (e) { out[name] = 'error:' + (e && e.message ? e.message : String(e)); }
}
attempt('updateEntry', function() { return testHost.anilist.updateEntry(1, 'CURRENT', null, 3); });
attempt('deleteEntry', function() { return testHost.anilist.deleteEntry(1); });
attempt('customQuery', function() { return testHost.anilist.customQuery({ query: 'mutation { DeleteMediaListEntry(id: 1) { deleted } }' }, ''); });
attempt('collection', function() { return testHost.anilist.collection('ANIME', false); });
attempt('localFiles', function() { return testHost.localFiles(); });
attempt('viewer', function() { return testHost.viewer(); });
attempt('token', function() { return testHost.token(); });
class Provider { report() { return out; } }`
	run := func(t *testing.T, scopes ...string) (map[string]string, []string) {
		t.Helper()
		m := newTestManager(t)
		rec := &hostRecorder{}
		m.Host = rec.services()
		man := pluginManifest("bindings", scopes...)
		h := &PluginHost{id: man.ID, mgr: m, man: man, scopes: pluginScopes(man)}
		rt := testRuntime(t, src, RuntimeOptions{Extra: func(r *Runtime, vm *goja.Runtime) error {
			return vm.Set("testHost", h.bindings(r, vm))
		}})
		raw, err := rt.CallProvider(context.Background(), "report")
		if err != nil {
			t.Fatal(err)
		}
		var out map[string]string
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
		return out, rec.Calls()
	}

	t.Run("no scopes", func(t *testing.T) {
		out, calls := run(t)
		for _, k := range []string{"updateEntry", "deleteEntry", "customQuery", "collection", "localFiles", "viewer"} {
			if !strings.HasPrefix(out[k], "error:") || !strings.Contains(out[k], "permission") {
				t.Errorf("%s = %q, want a permission error", k, out[k])
			}
		}
		if out["token"] != "ok:" {
			t.Errorf("token = %q, want empty", out["token"])
		}
		if len(calls) > 0 {
			t.Errorf("host services were called: %v", calls)
		}
	})
	t.Run("anilist", func(t *testing.T) {
		out, calls := run(t, "anilist")
		for _, k := range []string{"updateEntry", "deleteEntry", "customQuery", "collection"} {
			if !strings.HasPrefix(out[k], "ok:") {
				t.Errorf("%s = %q, want ok", k, out[k])
			}
		}
		if !strings.HasPrefix(out["localFiles"], "error:") {
			t.Errorf("localFiles = %q without the database scope", out["localFiles"])
		}
		// Without "anilist-token" a custom query must be anonymous.
		if !slices.Contains(calls, "customQuery:token=") {
			t.Errorf("calls = %v, want an anonymous custom query", calls)
		}
	})
	t.Run("anilist and token", func(t *testing.T) {
		_, calls := run(t, "anilist", "anilist-token")
		if !slices.Contains(calls, "customQuery:token=user-token") {
			t.Errorf("calls = %v, want the custom query to use the user's token", calls)
		}
	})
	t.Run("database", func(t *testing.T) {
		out, _ := run(t, "database")
		if out["localFiles"] != "ok:[]" || !strings.Contains(out["viewer"], "user") {
			t.Errorf("localFiles = %q, viewer = %q", out["localFiles"], out["viewer"])
		}
		if out["token"] != "ok:" {
			t.Errorf("token = %q without the anilist-token scope", out["token"])
		}
	})
	t.Run("database and token", func(t *testing.T) {
		out, _ := run(t, "database", "anilist-token")
		if out["token"] != "ok:user-token" {
			t.Errorf("token = %q", out["token"])
		}
	})
}

// ---------------------------------------------------------------------------
// Timeouts and shutdown

func TestProviderEndlessTopLevelLoopTimesOut(t *testing.T) {
	man := providerManifest("spin")
	prog, err := Compile(man, `while (true) {}`)
	if err != nil {
		t.Fatal(err)
	}
	var captured atomic.Pointer[Runtime]
	start := time.Now()
	_, err = NewRuntime(man, prog, RuntimeOptions{
		LoadTimeout: 200 * time.Millisecond,
		Extra: func(r *Runtime, vm *goja.Runtime) error {
			captured.Store(r)
			return nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "too long") {
		t.Fatalf("err = %v, want a load timeout", err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("NewRuntime took %s", d)
	}
	rt := captured.Load()
	if rt == nil {
		t.Fatal("runtime not captured")
	}
	if rt.RunOnLoop(func(*goja.Runtime) {}) {
		t.Error("the timed out runtime still accepts jobs")
	}
}

func TestCloseStopsBusyRuntime(t *testing.T) {
	bodies := map[string]string{
		"loop":  `while (true) {}`,
		"sleep": `$sleep(10 * 60 * 1000); while (true) {}`,
		// Terminate runs queued callbacks itself: they must be interrupted too.
		"immediate": `setImmediate(function() { while (true) {} }); while (true) {}`,
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			rt := testRuntime(t, `class Provider { busy() { $store.set('busy', true); `+body+` } }`, RuntimeOptions{})
			res := make(chan error, 1)
			go func() {
				_, err := rt.CallProvider(context.Background(), "busy")
				res <- err
			}()
			waitFor(t, 3*time.Second, "the method to start", func() bool {
				_, ok := storeRaw(rt.store, "busy")
				return ok
			})
			returnsWithin(t, 3*time.Second, "Close", rt.Close)
			select {
			case err := <-res:
				if err == nil {
					t.Error("CallProvider succeeded on a closed runtime")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("CallProvider still waiting after Close")
			}
			if rt.RunOnLoop(func(*goja.Runtime) {}) {
				t.Error("closed runtime still accepts jobs")
			}
		})
	}
}

func TestManagerNotBlockedByLoadingExtension(t *testing.T) {
	m := newTestManager(t)
	l := installTestExtension(t, m, providerManifest("stuck"), `while (true) {}`, false)
	started := make(chan error, 1)
	go func() {
		_, err := l.Runtime()
		started <- err
	}()
	waitFor(t, 3*time.Second, "the start to begin", func() bool {
		l.mu.Lock()
		defer l.mu.Unlock()
		return l.starting != nil
	})
	returnsWithin(t, time.Second, "List", func() { m.List() })
	returnsWithin(t, time.Second, "Logs", func() { m.Logs("stuck") })
	returnsWithin(t, 3*time.Second, "SetEnabled(false)", func() {
		if err := m.SetEnabled("stuck", false); err != nil {
			t.Error(err)
		}
	})
	select {
	case err := <-started:
		if err == nil {
			t.Error("the start should have been cancelled")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Runtime() still blocked after the extension was disabled")
	}
	if inf, _ := m.Get("stuck"); inf.Running {
		t.Error("disabled extension reported as running")
	}
}

func TestConcurrentRuntimeStartsShareOneRuntime(t *testing.T) {
	m := newTestManager(t)
	l := installTestExtension(t, m, providerManifest("shared"),
		`$store.set('load-' + Math.random(), 1); $sleep(50); class Provider {}`, false)
	const n = 8
	rts := make([]*Runtime, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rts[i], errs[i] = l.Runtime()
		}(i)
	}
	wg.Wait()
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("Runtime() #%d: %v", i, errs[i])
		}
		if rts[i] != rts[0] {
			t.Fatalf("Runtime() #%d returned a different runtime", i)
		}
	}
	s := m.store("shared")
	s.mu.Lock()
	loads := 0
	for k := range s.data {
		if strings.HasPrefix(k, "load-") {
			loads++
		}
	}
	s.mu.Unlock()
	if loads != 1 {
		t.Errorf("payload ran %d times, want 1", loads)
	}
}

func TestUninstallClosesRuntimeAndPreventsRestart(t *testing.T) {
	m := newTestManager(t)
	l := installTestExtension(t, m, providerManifest("gone"), `class Provider { ping() { return 1; } }`, false)
	rt, err := l.Runtime()
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Uninstall("gone"); err != nil {
		t.Fatal(err)
	}
	if rt.RunOnLoop(func(*goja.Runtime) {}) {
		t.Error("the runtime of an uninstalled extension is still running")
	}
	// A provider handle obtained before the uninstall must not restart it.
	if _, err := l.Runtime(); err == nil {
		t.Error("an uninstalled extension was restarted")
	}
}

// ---------------------------------------------------------------------------
// Plugin runtime leaks

func TestPluginInitFailureClosesRuntime(t *testing.T) {
	m := newTestManager(t)
	l := installTestExtension(t, m, pluginManifest("failing"), `
var beats = 0;
function init() {
  setInterval(function() { $store.set('beat', ++beats); }, 2);
  console.log('about to fail');
  throw new Error('boom');
}`, true)
	m.startPlugin(l)

	inf, _ := m.Get("failing")
	if !strings.Contains(inf.Error, "boom") {
		t.Errorf("error = %q", inf.Error)
	}
	if inf.Running {
		t.Error("failed plugin reported as running")
	}
	if n := len(m.runningPlugins()); n != 0 {
		t.Errorf("%d plugin hosts registered", n)
	}
	a, _ := storeRaw(m.store("failing"), "beat")
	time.Sleep(50 * time.Millisecond)
	if b, _ := storeRaw(m.store("failing"), "beat"); a != b {
		t.Error("the failed plugin's timers are still running")
	}
	logs := ""
	for _, line := range m.Logs("failing") {
		logs += line.Message + "\n"
	}
	if !strings.Contains(logs, "about to fail") || !strings.Contains(logs, "boom") {
		t.Errorf("logs of the failed start were lost: %q", logs)
	}
}

func TestPluginStartTimeoutClosesRuntime(t *testing.T) {
	payloads := map[string]string{
		"toplevel": `while (true) {}`,
		"init":     `function init() { while (true) {} }`,
		"sleep":    `function init() { $sleep(10 * 60 * 1000); }`,
	}
	for name, payload := range payloads {
		t.Run(name, func(t *testing.T) {
			m := newTestManager(t)
			m.loadTimeout = 200 * time.Millisecond
			l := installTestExtension(t, m, pluginManifest("slow-"+name), payload, true)
			returnsWithin(t, 3*time.Second, "startPlugin", func() { m.startPlugin(l) })
			inf, _ := m.Get(l.Manifest.ID)
			if !strings.Contains(inf.Error, "too long") {
				t.Errorf("error = %q", inf.Error)
			}
			if inf.Running {
				t.Error("timed out plugin reported as running")
			}
		})
	}
}

const heartbeatPlugin = `
var me = String(Math.random()), beats = 0;
setInterval(function() { $store.set('beat:' + me, ++beats); }, 2);
function init() {}`

func TestConcurrentPluginStartsLeaveOneRuntime(t *testing.T) {
	m := newTestManager(t)
	l := installTestExtension(t, m, pluginManifest("multi"), heartbeatPlugin, true)
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.startPlugin(l)
		}()
	}
	wg.Wait()
	if n := len(m.runningPlugins()); n != 1 {
		t.Fatalf("%d plugin hosts registered, want 1", n)
	}
	if alive := liveHeartbeats(m.store("multi")); alive != 1 {
		t.Fatalf("%d runtimes running, want 1", alive)
	}
	m.stopPlugin("multi")
	if alive := liveHeartbeats(m.store("multi")); alive != 0 {
		t.Fatalf("%d runtimes running after stop, want 0", alive)
	}
}

func TestPluginToggleEndsInLastState(t *testing.T) {
	m := newTestManager(t)
	installTestExtension(t, m, pluginManifest("toggle"), heartbeatPlugin, true)
	// Each enable starts the plugin in the background.
	for i := 0; i < 5; i++ {
		if err := m.SetEnabled("toggle", true); err != nil {
			t.Fatal(err)
		}
		if err := m.Grant("toggle"); err != nil {
			t.Fatal(err)
		}
		if err := m.SetEnabled("toggle", false); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(300 * time.Millisecond) // let the background starts run
	if n := len(m.runningPlugins()); n != 0 {
		t.Errorf("%d plugin hosts registered for a disabled plugin", n)
	}
	if alive := liveHeartbeats(m.store("toggle")); alive != 0 {
		t.Errorf("%d runtimes running for a disabled plugin", alive)
	}
}

// ---------------------------------------------------------------------------
// Sandbox robustness

// Hostile values used to crash the whole process: goja doesn't turn Go
// runtime panics (nil Value, slice bounds) in host functions into JS
// exceptions, and timer callbacks run without a recover.
func TestHostHelpersSurviveHostileValues(t *testing.T) {
	rt := testRuntime(t, `
var results = {};
function check(name, fn) {
  try { results[name] = 'ok:' + String(fn()); } catch (e) { results[name] = 'threw'; }
}
setTimeout(function() {
  check('fakeBuffer', function() { return $toString({ buffer: new ArrayBuffer(4) }); });
  check('badOffsets', function() { return $toString({ buffer: new ArrayBuffer(4), byteOffset: -1, byteLength: 2 }); });
  check('hugeOffsets', function() { return $toString({ buffer: new ArrayBuffer(4), byteOffset: 9007199254740991, byteLength: 9007199254740991 }); });
  check('docPlainObject', function() { return LoadDoc('<p>x</p>')({}).length(); });
  check('shortIV', function() { return CryptoJS.AES.encrypt('x', 'key', { iv: 'short' }).toString(); });
  check('vanishingMessage', function() {
    var o = { stack: 's' };
    Object.defineProperty(o, 'message', { configurable: true, get: function() { delete o.message; return 'm'; } });
    console.log(o);
    return 1;
  });
  var settled = [];
  function settle(name, p) {
    settled.push(p.then(function() { results[name] = 'resolved'; }, function() { results[name] = 'rejected'; }));
  }
  settle('vanishingHeader', fetch('http://127.0.0.1/', { headers: { get a() { delete this.b; return 'x'; }, b: 'y' } }));
  var J = JSON;
  delete globalThis.JSON;
  check('logWithoutJSON', function() { console.log({ a: 1 }); return 1; });
  check('toStringWithoutJSON', function() { return $toString({ a: 1 }); });
  globalThis.JSON = 1;
  check('storeWithBadJSON', function() { $store.set('x', { a: 1 }); return 1; });
  globalThis.JSON = J;
  settle('fakeFormData', fetch('http://127.0.0.1/', { method: 'POST', body: { __kumoFormData: true } }));
  settle('hugeFormData', fetch('http://127.0.0.1/', { method: 'POST', body: { __kumoFormData: true, _entries: { length: 1e12 } } }));
  Promise.all(settled).then(function() { $store.set('results', results); });
}, 0);
class Provider {}`, RuntimeOptions{})
	waitFor(t, 3*time.Second, "the checks to run", func() bool {
		_, ok := storeRaw(rt.store, "results")
		return ok
	})
	raw, _ := storeRaw(rt.store, "results")
	var results map[string]string
	if err := json.Unmarshal([]byte(raw), &results); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"fakeBuffer": "ok:", "badOffsets": "ok:", "hugeOffsets": "ok:", "docPlainObject": "ok:0",
		"shortIV": "threw", "vanishingMessage": "ok:1", "logWithoutJSON": "ok:1", "toStringWithoutJSON": "ok:",
		"storeWithBadJSON": "ok:1", "fakeFormData": "rejected", "hugeFormData": "rejected", "vanishingHeader": "rejected",
	}
	for k, prefix := range want {
		if !strings.HasPrefix(results[k], prefix) {
			t.Errorf("%s = %q, want %q...", k, results[k], prefix)
		}
	}
}

func TestHasMethodWithoutProviderClass(t *testing.T) {
	for _, src := range []string{
		`var Provider = null;`,
		`var Provider = 1;`,
		`var Provider = { prototype: null };`,
		`function Provider() {} Provider.prototype = undefined;`,
		`class Provider { get search() { throw new Error('no'); } }`,
	} {
		if testRuntime(t, src, RuntimeOptions{}).HasMethod("search") {
			t.Errorf("%s: HasMethod = true", src)
		}
	}
	if !testRuntime(t, `class Provider { search() {} }`, RuntimeOptions{}).HasMethod("search") {
		t.Error("HasMethod = false for a real method")
	}
}

func TestRequireCannotReadFiles(t *testing.T) {
	p := filepath.Join(t.TempDir(), "secret.json")
	if err := os.WriteFile(p, []byte(`{"token":"s3cret"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	rt := testRuntime(t, fmt.Sprintf(`
var leaked = null, builtin = typeof require('buffer').Buffer;
try { leaked = require(%q).token; } catch (e) {}
class Provider { get() { return { leaked: leaked, builtin: builtin }; } }`, p), RuntimeOptions{})
	raw, err := rt.CallProvider(context.Background(), "get")
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"leaked":null,"builtin":"function"}` {
		t.Errorf("got %s", raw)
	}
}

// Providers (no plugin prelude) keep working end to end.
func TestProviderStillWorks(t *testing.T) {
	m := newTestManager(t)
	installTestExtension(t, m, providerManifest("works"), `
class Provider {
  async search(opts) {
    await new Promise(function(resolve) { setTimeout(resolve, 1); });
    $storage.set('last', opts.query);
    return [{ id: opts.query + ':' + $storage.get('last'), title: 'T', url: 'u', subOrDub: 'sub' }];
  }
}`, false)
	p, err := m.OnlineStreamProvider("works")
	if err != nil {
		t.Fatal(err)
	}
	res, err := p.Search(context.Background(), nil, "frieren", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].ID != "frieren:frieren" {
		t.Errorf("results = %+v", res)
	}
}
