package extensions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"

	"github.com/simo1337s/animetest/server/internal/events"
)

// PluginHostServices are the app features plugins can call. Each function
// may be nil when unavailable.
type PluginHostServices struct {
	GetAnime        func(ctx context.Context, id int) (any, error)
	GetAnimeDetails func(ctx context.Context, id int) (any, error)
	GetManga        func(ctx context.Context, id int) (any, error)
	Collection      func(ctx context.Context, mediaType string, refresh bool) (any, error)
	UpdateEntry     func(ctx context.Context, mediaID int, status *string, score *float64, progress *int) error
	DeleteEntry     func(ctx context.Context, mediaID int) error
	CustomQuery     func(ctx context.Context, body map[string]any, token string) (any, error)
	ListAnime       func(ctx context.Context, page int, search string, perPage int) (any, error)
	Token           func() string
	Viewer          func() (name, avatar string)
	AnimeEntry      func(ctx context.Context, id int) (any, error)
	LocalFiles      func() (any, error)
}

// PluginUIState is what a plugin currently shows (trays, page actions...).
type PluginUIState struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Icon  string          `json:"icon"`
	State json.RawMessage `json:"state"`
}

// PluginHost runs one plugin in a single VM: everything shares it (rather
// than re-running callbacks from source in separate VMs), which is a
// compatible superset of what plugins expect.
type PluginHost struct {
	id     string
	mgr    *Manager
	man    *Manifest
	scopes map[string]bool // granted permission scopes
	// rt is set before the host is registered in Manager.plugins and never
	// changes afterwards.
	rt *Runtime

	// Entry points returned by the prelude; only used on the loop goroutine.
	startUI, runHook, runEvent goja.Callable

	mu    sync.Mutex
	state json.RawMessage
}

func (h *PluginHost) Snapshot() PluginUIState {
	h.mu.Lock()
	defer h.mu.Unlock()
	st := h.state
	if st == nil {
		st = json.RawMessage(`{}`)
	}
	return PluginUIState{ID: h.id, Name: h.man.Name, Icon: h.man.Icon, State: st}
}

func pluginScopes(man *Manifest) map[string]bool {
	scopes := map[string]bool{}
	if man.Plugin != nil {
		for _, s := range man.Plugin.Permissions.Scopes {
			scopes[s] = true
		}
	}
	return scopes
}

// pluginSlot serializes starting and stopping one plugin, so overlapping
// requests (Grant, SetEnabled and SaveUserConfig each start it in the
// background) can't leave an orphaned runtime running.
type pluginSlot struct {
	run sync.Mutex // held for the whole start or stop

	// Guarded by Manager.pluginMu.
	gen    uint64             // bumped by every start or stop request
	cancel context.CancelFunc // aborts the start in progress
}

// pluginRequest registers a start or stop request. The newest request wins:
// the start in progress, if any, is cancelled and older requests still
// waiting for the slot give up.
func (m *Manager) pluginRequest(id string) (*pluginSlot, uint64) {
	m.pluginMu.Lock()
	defer m.pluginMu.Unlock()
	s := m.pluginSlots[id]
	if s == nil {
		s = &pluginSlot{}
		m.pluginSlots[id] = s
	}
	s.gen++
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	return s, s.gen
}

// startPlugin (re)starts a plugin, provided it is still installed, enabled
// and granted when its turn comes.
func (m *Manager) startPlugin(l *Loaded) {
	id := l.Manifest.ID
	slot, gen := m.pluginRequest(id)
	slot.run.Lock()
	defer slot.run.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if !m.takeSlot(slot, gen, cancel) {
		return
	}
	m.stopPluginLocked(id)
	if m.pluginWanted(l) {
		m.launchPlugin(ctx, l)
	}
	m.pluginMu.Lock()
	if slot.gen == gen {
		slot.cancel = nil
	}
	m.pluginMu.Unlock()
}

// stopPlugin stops a plugin, aborting a start in progress.
func (m *Manager) stopPlugin(id string) {
	slot, gen := m.pluginRequest(id)
	slot.run.Lock()
	defer slot.run.Unlock()
	if m.takeSlot(slot, gen, nil) {
		m.stopPluginLocked(id)
	}
}

// takeSlot reports whether the request is still the newest one, now that it
// holds the slot; only the newest request acts, so an older one can't undo
// what a newer one did. cancel (if any) becomes the way to abort it.
func (m *Manager) takeSlot(slot *pluginSlot, gen uint64, cancel context.CancelFunc) bool {
	m.pluginMu.Lock()
	defer m.pluginMu.Unlock()
	if slot.gen != gen {
		return false
	}
	slot.cancel = cancel
	return true
}

// stopPluginLocked stops the running instance of a plugin. The caller holds
// the plugin's slot.
func (m *Manager) stopPluginLocked(id string) {
	m.pluginMu.Lock()
	h := m.plugins[id]
	delete(m.plugins, id)
	m.pluginMu.Unlock()
	if h != nil {
		h.rt.Close()
		m.hub.Publish(events.PluginUI, map[string]any{"pluginId": id, "type": "removed"})
	}
}

// pluginWanted reports whether l should be running. Starts happen in the
// background, so this is checked again once a start holds the slot.
func (m *Manager) pluginWanted(l *Loaded) bool {
	l.mu.Lock()
	ok := l.Enabled && !l.removed
	l.mu.Unlock()
	if !ok {
		return false
	}
	cur, installed := m.get(l.Manifest.ID)
	return installed && cur == l && m.granted(l)
}

// launchPlugin starts the plugin's runtime and runs its init. If anything
// fails (or ctx is cancelled by a newer request) the runtime is closed and
// unregistered. The caller holds the plugin's slot.
func (m *Manager) launchPlugin(ctx context.Context, l *Loaded) {
	man := l.Manifest
	h := &PluginHost{id: man.ID, mgr: m, man: man, scopes: pluginScopes(man)}
	var rt *Runtime
	fail := func(err error) {
		var logs []LogLine
		if rt != nil {
			m.pluginMu.Lock()
			if m.plugins[man.ID] == h {
				delete(m.plugins, man.ID)
			}
			m.pluginMu.Unlock()
			rt.Close()
			m.hub.Publish(events.PluginUI, map[string]any{"pluginId": man.ID, "type": "removed"})
			logs = rt.Logs()
		}
		if ctx.Err() != nil {
			return // superseded by a newer start or a stop: not an error
		}
		logs = append(logs, LogLine{Time: time.Now().Unix(), Level: "error", Message: err.Error()})
		l.mu.Lock()
		l.err = err.Error()
		l.lastLogs = logs
		l.mu.Unlock()
		log.Printf("plugin %s: %v", man.ID, err)
		m.hub.Publish(events.ExtensionsUpdate, nil)
	}

	l.mu.Lock()
	cfg := l.UserConfig
	l.mu.Unlock()
	payload, prefs, cfgErr := ApplyUserConfig(man, l.payload, &cfg)
	l.setConfigError(cfgErr)
	prog, err := Compile(man, payload)
	if err != nil {
		fail(err)
		return
	}
	domains := append([]string{}, builtinPluginDomains...)
	if man.Plugin != nil {
		na := man.Plugin.Permissions.Allow.NetworkAccess
		for _, d := range na.AllowedDomains {
			if d == "*" && na.Reasoning == "" {
				continue
			}
			domains = append(domains, d)
		}
	}
	var storage *Storage
	if h.scopes["storage"] {
		storage = NewStorage(m.db, man.ID)
	}
	rt, err = NewRuntime(man, prog, RuntimeOptions{
		Context:     ctx,
		LoadTimeout: m.loadTimeout,
		Prefs:       prefs,
		Store:       m.store(man.ID),
		Storage:     storage,
		Fetcher:     m.fetcher.WithDomains(domains),
		Extra:       h.install,
	})
	if err != nil {
		fail(err) // rt is nil: NewRuntime closed it
		return
	}
	h.rt = rt
	m.pluginMu.Lock()
	m.plugins[man.ID] = h
	m.pluginMu.Unlock()
	if err := h.init(ctx, m.loadTimeout); err != nil {
		fail(err)
		return
	}
	l.mu.Lock()
	l.err = ""
	l.lastLogs = nil
	l.mu.Unlock()
	// Give plugins that hook collection events something to work with.
	go h.fireCollectionHooks()
	m.hub.Publish(events.ExtensionsUpdate, nil)
}

// init runs the plugin's init() and renders its UI.
func (h *PluginHost) init(ctx context.Context, timeout time.Duration) error {
	done := make(chan error, 1)
	ok := h.rt.RunOnLoop(func(vm *goja.Runtime) {
		defer func() {
			if p := recover(); p != nil {
				done <- fmt.Errorf("panic: %v", p)
			}
		}()
		if fn, ok := goja.AssertFunction(vm.Get("init")); ok {
			if _, err := fn(goja.Undefined()); err != nil {
				done <- jsError(err)
				return
			}
		}
		_, err := h.startUI(goja.Undefined())
		done <- jsError(err)
	})
	if !ok {
		return errStopped
	}
	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case err := <-done:
		return err
	case <-t.C:
		return errors.New("plugin took too long to start")
	case <-ctx.Done():
		return errStopped
	case <-h.rt.ctx.Done():
		return errStopped
	}
}

func (m *Manager) runningPlugins() []*PluginHost {
	m.pluginMu.Lock()
	defer m.pluginMu.Unlock()
	hosts := make([]*PluginHost, 0, len(m.plugins))
	for _, h := range m.plugins {
		hosts = append(hosts, h)
	}
	return hosts
}

// PluginStates returns the UI state of every running plugin.
func (m *Manager) PluginStates() []PluginUIState {
	out := []PluginUIState{}
	for _, h := range m.runningPlugins() {
		out = append(out, h.Snapshot())
	}
	return out
}

// DispatchPluginEvent forwards a UI event (button click, field change, tray
// open, navigation...) to a plugin.
func (m *Manager) DispatchPluginEvent(id string, evt map[string]any) error {
	m.pluginMu.Lock()
	h := m.plugins[id]
	m.pluginMu.Unlock()
	if h == nil {
		return errors.New("plugin is not running")
	}
	h.dispatch(evt)
	return nil
}

// BroadcastPluginEvent sends an event (e.g. navigation) to every plugin.
func (m *Manager) BroadcastPluginEvent(evt map[string]any) {
	for _, h := range m.runningPlugins() {
		h.dispatch(evt)
	}
}

// FireHook runs $app.on<Hook> callbacks registered by plugins.
func (m *Manager) FireHook(name string, data any) {
	raw, err := json.Marshal(data)
	if err != nil {
		return
	}
	for _, h := range m.runningPlugins() {
		h.fireHook(name, raw)
	}
}

func (h *PluginHost) dispatch(evt map[string]any) {
	raw, _ := json.Marshal(evt)
	h.rt.RunOnLoop(func(vm *goja.Runtime) {
		if _, err := h.runEvent(goja.Undefined(), vm.ToValue(string(raw))); err != nil {
			h.rt.log("error", "event: "+jsError(err).Error())
		}
	})
}

func (h *PluginHost) fireHook(name string, raw []byte) {
	h.rt.RunOnLoop(func(vm *goja.Runtime) {
		if _, err := h.runHook(goja.Undefined(), vm.ToValue(name), vm.ToValue(string(raw))); err != nil {
			h.rt.log("error", name+": "+jsError(err).Error())
		}
	})
}

func (h *PluginHost) fireCollectionHooks() {
	svc := h.mgr.Host
	if svc.Collection == nil {
		return
	}
	ctx, cancel := context.WithTimeout(h.rt.ctx, 30*time.Second)
	defer cancel()
	c, err := svc.Collection(ctx, "ANIME", false)
	if err != nil {
		return
	}
	raw, err := json.Marshal(map[string]any{"animeCollection": c})
	if err != nil {
		return
	}
	h.fireHook("onGetAnimeCollection", raw)
	h.fireHook("onGetRawAnimeCollection", raw)
}

// install builds the host bindings and runs the plugin prelude. The bindings
// are passed to the prelude as an argument instead of being a global, so
// plugin code (which runs after the prelude) can only reach them through the
// APIs the prelude builds for the granted scopes.
func (h *PluginHost) install(r *Runtime, vm *goja.Runtime) error {
	v, err := vm.RunString(pluginPrelude)
	if err != nil {
		return jsError(err)
	}
	prelude, ok := goja.AssertFunction(v)
	if !ok {
		return errors.New("plugin prelude is not a function")
	}
	ret, err := prelude(goja.Undefined(), h.bindings(r, vm))
	if err != nil {
		return jsError(err)
	}
	entry, ok := ret.(*goja.Object)
	if !ok {
		return errors.New("plugin prelude returned no entry points")
	}
	var ok1, ok2, ok3 bool
	h.startUI, ok1 = goja.AssertFunction(entry.Get("startUI"))
	h.runHook, ok2 = goja.AssertFunction(entry.Get("hook"))
	h.runEvent, ok3 = goja.AssertFunction(entry.Get("dispatch"))
	if !ok1 || !ok2 || !ok3 {
		return errors.New("plugin prelude returned incomplete entry points")
	}
	return nil
}

// bindings returns the Go services the prelude builds the plugin APIs on.
// Bindings behind a scope in the prelude ($anilist, $database) check that
// scope themselves too, so the scopes hold even if plugin code got hold of
// this object.
func (h *PluginHost) bindings(r *Runtime, vm *goja.Runtime) *goja.Object {
	svc := h.mgr.Host
	ctx := r.ctx // cancelled when the plugin stops
	// No prototype: a getter a plugin defines on Object.prototype must never
	// see these objects as `this`.
	host := vm.NewObject()
	_ = host.SetPrototype(nil)
	_ = host.Set("pluginId", h.id)
	_ = host.Set("emit", func(typ string, payload goja.Value) {
		if r.closed.Load() {
			return // a stopping runtime must not overwrite its successor's UI
		}
		raw, _ := stringifyJS(vm, payload)
		if typ == "state" {
			h.mu.Lock()
			h.state = raw
			h.mu.Unlock()
		}
		h.mgr.hub.Publish(events.PluginUI, map[string]any{"pluginId": h.id, "type": typ, "payload": raw})
	})
	_ = host.Set("toast", func(level, msg string) {
		h.mgr.hub.Publish(events.Toast, events.ToastPayload{Level: level, Message: msg})
	})
	_ = host.Set("hasScope", func(s string) bool { return h.scopes[s] })

	result := func(v any, err error) goja.Value {
		if err != nil {
			panic(vm.NewGoError(err))
		}
		return toJS(vm, v)
	}
	unsupported := func(name string) error { return fmt.Errorf("%s is not available in Kumo", name) }
	needScope := func(api, scope string) {
		if !h.scopes[scope] {
			panic(vm.NewGoError(fmt.Errorf("%s requires the %q permission", api, scope)))
		}
	}

	// $anilist ("anilist" scope).
	al := vm.NewObject()
	_ = al.SetPrototype(nil)
	_ = al.Set("getAnime", func(id int) goja.Value {
		needScope("$anilist", "anilist")
		if svc.GetAnime == nil {
			return result(nil, unsupported("getAnime"))
		}
		return result(svc.GetAnime(ctx, id))
	})
	_ = al.Set("getAnimeDetails", func(id int) goja.Value {
		needScope("$anilist", "anilist")
		if svc.GetAnimeDetails == nil {
			return result(nil, unsupported("getAnimeDetails"))
		}
		return result(svc.GetAnimeDetails(ctx, id))
	})
	_ = al.Set("getManga", func(id int) goja.Value {
		needScope("$anilist", "anilist")
		if svc.GetManga == nil {
			return result(nil, unsupported("getManga"))
		}
		return result(svc.GetManga(ctx, id))
	})
	_ = al.Set("collection", func(mediaType string, refresh bool) goja.Value {
		needScope("$anilist", "anilist")
		if svc.Collection == nil {
			return result(nil, unsupported("collections"))
		}
		return result(svc.Collection(ctx, mediaType, refresh))
	})
	_ = al.Set("updateEntry", func(call goja.FunctionCall) goja.Value {
		needScope("$anilist", "anilist")
		if svc.UpdateEntry == nil {
			return result(nil, unsupported("updateEntry"))
		}
		id := int(call.Argument(0).ToInteger())
		var status *string
		var score *float64
		var progress *int
		if v := call.Argument(1); !goja.IsUndefined(v) && !goja.IsNull(v) {
			s := v.String()
			status = &s
		}
		if v := call.Argument(2); !goja.IsUndefined(v) && !goja.IsNull(v) {
			f := v.ToFloat()
			score = &f
		}
		if v := call.Argument(3); !goja.IsUndefined(v) && !goja.IsNull(v) {
			p := int(v.ToInteger())
			progress = &p
		}
		return result(nil, svc.UpdateEntry(ctx, id, status, score, progress))
	})
	_ = al.Set("deleteEntry", func(id int) goja.Value {
		needScope("$anilist", "anilist")
		if svc.DeleteEntry == nil {
			return result(nil, unsupported("deleteEntry"))
		}
		return result(nil, svc.DeleteEntry(ctx, id))
	})
	_ = al.Set("customQuery", func(body map[string]any, token string) goja.Value {
		needScope("$anilist", "anilist")
		if svc.CustomQuery == nil {
			return result(nil, unsupported("customQuery"))
		}
		// As plugins expect, a query only carries the token it is given (none
		// means anonymous). Plugins allowed to use the user's token may
		// leave it out to query as the user.
		if token == "" && h.scopes["anilist-token"] && svc.Token != nil {
			token = svc.Token()
		}
		return result(svc.CustomQuery(ctx, body, token))
	})
	_ = al.Set("listAnime", func(page int, search string, perPage int) goja.Value {
		needScope("$anilist", "anilist")
		if svc.ListAnime == nil {
			return result(nil, unsupported("listAnime"))
		}
		return result(svc.ListAnime(ctx, page, search, perPage))
	})
	_ = host.Set("anilist", al)

	// ctx.anime / ctx.manga are available to every plugin, as plugins expect.
	_ = host.Set("animeEntry", func(id int) goja.Value {
		if svc.AnimeEntry == nil {
			return result(nil, unsupported("getAnimeEntry"))
		}
		return result(svc.AnimeEntry(ctx, id))
	})
	_ = host.Set("mangaCollection", func() goja.Value {
		if svc.Collection == nil {
			return result(nil, unsupported("collections"))
		}
		return result(svc.Collection(ctx, "MANGA", false))
	})
	_ = host.Set("manga", func(id int) goja.Value {
		if svc.GetManga == nil {
			return result(nil, unsupported("getManga"))
		}
		return result(svc.GetManga(ctx, id))
	})

	// $database ("database" scope; the token also needs "anilist-token").
	_ = host.Set("token", func() string {
		if svc.Token == nil || !h.scopes["database"] || !h.scopes["anilist-token"] {
			return ""
		}
		return svc.Token()
	})
	_ = host.Set("viewer", func() goja.Value {
		needScope("$database", "database")
		if svc.Viewer == nil {
			return goja.Null()
		}
		name, avatar := svc.Viewer()
		return toJS(vm, map[string]string{"name": name, "avatar": avatar})
	})
	_ = host.Set("localFiles", func() goja.Value {
		needScope("$database", "database")
		if svc.LocalFiles == nil {
			return result([]any{}, nil)
		}
		return result(svc.LocalFiles())
	})
	return host
}

// The plugin context is implemented in JavaScript on top of the host
// bindings. The script evaluates to a function that takes the bindings and
// returns the entry points Go calls, so neither becomes a global that plugin
// code could reach.
var pluginPrelude = strings.TrimSpace(`
(function(host){
  var g = globalThis;
  var uid = 0;
  function nid(p){ return (p || 'n') + (++uid); }
  var handlers = {}, fieldRefs = {}, effects = [], trays = [], actions = [], webviews = [], palettes = [];
  var navCallbacks = [], uiCallbacks = [], hooks = {}, shared = {};
  var screenState = { pathname: '/', searchParams: {} };
  var renderQueued = false;

  function safe(fn, label){ return function(){ try { return fn.apply(this, arguments); } catch (e) { console.error((label || 'plugin') + ': ' + (e && e.message ? e.message : e)); } }; }

  function render(){
    renderQueued = false;
    var state = { trays: [], actions: [], webviews: [] };
    trays.forEach(function(t){
      var tree = null;
      if (t.renderFn) { try { tree = t.renderFn(); } catch (e) { console.error('tray render: ' + (e && e.message ? e.message : e)); tree = { type: 'alert', props: { title: 'Plugin error', description: String(e && e.message ? e.message : e), intent: 'alert' } }; } }
      state.trays.push({ id: t.id, iconUrl: t.opts.iconUrl || '', tooltipText: t.opts.tooltipText || '', withContent: t.opts.withContent !== false,
        width: t.opts.width || '', minHeight: t.opts.minHeight || '', badge: t.badge, tree: tree });
    });
    actions.forEach(function(a){ if (a.mounted) state.actions.push({ id: a.id, kind: a.kind, props: a.props }); });
    webviews.forEach(function(w){
      var html = '';
      if (w.contentFn) { try { html = w.contentFn(); } catch (e) { html = '<pre>' + String(e) + '</pre>'; } }
      state.webviews.push({ id: w.id, options: w.opts, hidden: w.hidden, html: html });
    });
    host.emit('state', state);
  }
  function scheduleRender(){ if (!renderQueued) { renderQueued = true; setTimeout(render, 0); } }

  // ---- reactive state
  function makeState(init){
    var s = { __state: true, _v: init };
    Object.defineProperty(s, 'value', { get: function(){ return s._v; }, set: function(v){ s.set(v); }, enumerable: true });
    s.get = function(){ return s._v; };
    s.set = function(v){
      var next = typeof v === 'function' ? v(s._v) : v;
      if (next === s._v && (next === null || typeof next !== 'object')) return;
      s._v = next;
      effects.forEach(function(e){ if (e.deps.indexOf(s) >= 0) setTimeout(safe(e.fn, 'effect'), 0); });
      scheduleRender();
    };
    return s;
  }
  function effect(fn, deps){ var e = { fn: fn, deps: deps || [] }; effects.push(e); return function(){ effects = effects.filter(function(x){ return x !== e; }); }; }
  function computed(fn, deps){ var s = makeState(fn()); effect(function(){ s.set(fn()); }, deps); return s; }
  function fieldRef(def){
    var r = { __ref: nid('f'), current: def, _cbs: [] };
    r.setValue = function(v){ r.current = v; scheduleRender(); };
    r.onValueChange = function(cb){ r._cbs.push(cb); };
    fieldRefs[r.__ref] = r;
    return r;
  }

  // ---- components
  var shorthand = { div: 'items', flex: 'items', stack: 'items', p: 'items', a: 'items', tabs: 'items', tabsList: 'items', tabsContent: 'items',
    text: 'text', span: 'text', badge: 'text', css: 'css', button: 'label', anchor: 'text', input: 'label', select: 'label', checkbox: 'label',
    radioGroup: 'label', 'switch': 'label', tooltip: 'item', dropdownMenuItem: 'item', dropdownMenuLabel: 'label', tabsTrigger: 'item', img: 'src',
    modal: null, dropdownMenu: null, popover: null, alert: null, dropdownMenuSeparator: null };
  function normalize(v){
    if (v && v.__ref) return v.__ref;
    if (v && v.__state) return v._v;
    return v;
  }
  function mk(type){
    return function(a, b){
      var props = {};
      var key = shorthand[type];
      if (key && (typeof a === 'string' || Array.isArray(a) || (key === 'item' && a && a.type))) { props[key] = a; if (b) Object.assign(props, b); }
      else if (a && typeof a === 'object') { Object.assign(props, a); }
      if (props.fieldRef && props.fieldRef.__ref) { var ref = props.fieldRef; props.fieldRef = ref.__ref; if (props.value === undefined) props.value = ref.current; }
      Object.keys(props).forEach(function(k){ props[k] = normalize(props[k]); });
      if ((type === 'flex' || type === 'stack') && props.gap === undefined) props.gap = 2;
      if (type === 'flex' && !props.direction) props.direction = 'row';
      if (type === 'button') { if (props.disabled === undefined) props.disabled = false; if (props.loading === undefined) props.loading = false; }
      if (Array.isArray(props.items)) props.items = props.items.filter(function(x){ return x !== null && x !== undefined && x !== false; });
      return { id: nid('c'), type: type, props: props };
    };
  }
  function withComponents(o){ Object.keys(shorthand).forEach(function(k){ o[k] = mk(k); }); return o; }

  // ---- trays
  function newTray(opts){
    var t = withComponents({ id: nid('t'), opts: opts || {}, renderFn: null, badge: null, _open: [], _close: [], _click: [] });
    t.render = function(fn){ t.renderFn = fn; scheduleRender(); };
    t.htm = function(fn){ t.renderFn = function(){ return { id: nid('c'), type: 'html', props: { html: String(fn()) } }; }; scheduleRender(); };
    t.update = scheduleRender;
    t.open = function(){ host.emit('tray-open', { trayId: t.id }); };
    t.close = function(){ host.emit('tray-close', { trayId: t.id }); };
    t.onOpen = function(cb){ t._open.push(cb); };
    t.onClose = function(cb){ t._close.push(cb); };
    t.onClick = function(cb){ t._click.push(cb); };
    t.updateBadge = function(b){ t.badge = b && typeof b === 'object' ? b : { number: b }; scheduleRender(); };
    trays.push(t); scheduleRender();
    return t;
  }

  // ---- page actions
  function newAction(kind, props){
    var a = { id: nid('a'), kind: kind, props: Object.assign({ label: '' }, props || {}), mounted: false, _click: [] };
    function setter(k){ return function(v){ a.props[k] = v; scheduleRender(); }; }
    a.mount = function(){ a.mounted = true; scheduleRender(); };
    a.unmount = function(){ a.mounted = false; scheduleRender(); };
    a.setLabel = setter('label'); a.setLoading = setter('loading'); a.setDisabled = setter('disabled');
    a.setStyle = setter('style'); a.setIntent = setter('intent'); a.setTooltipText = setter('tooltipText'); a.setFor = setter('for');
    a.onClick = function(cb){ a._click.push(cb); };
    actions.push(a);
    return a;
  }

  // ---- webviews (rendered in an iframe by the UI)
  function newWebview(opts){
    var w = withComponents({ id: nid('w'), opts: opts || {}, hidden: !!(opts && opts.hidden), contentFn: null, _on: {}, _mount: [], _load: [], _unmount: [] });
    w.setContent = function(fn){ w.contentFn = typeof fn === 'function' ? fn : function(){ return String(fn); }; scheduleRender(); };
    w.update = scheduleRender;
    w.setOptions = function(o){ Object.assign(w.opts, o); scheduleRender(); };
    w.show = function(){ w.hidden = false; scheduleRender(); };
    w.hide = function(){ w.hidden = true; scheduleRender(); };
    w.isHidden = function(){ return w.hidden; };
    w.close = function(){ w.hidden = true; scheduleRender(); };
    w.getScreenPath = function(){ return screenState.pathname; };
    w.onMount = function(cb){ w._mount.push(cb); }; w.onLoad = function(cb){ w._load.push(cb); }; w.onUnmount = function(cb){ w._unmount.push(cb); };
    w.channel = {
      on: function(name, cb){ (w._on[name] = w._on[name] || []).push(cb); },
      send: function(name, payload){ host.emit('webview-message', { webviewId: w.id, name: name, payload: payload }); },
      sync: function(name, st){ var push = function(){ host.emit('webview-message', { webviewId: w.id, name: name, payload: st.get ? st.get() : st }); }; if (st && st.__state) effect(push, [st]); push(); }
    };
    webviews.push(w); scheduleRender();
    return w;
  }

  // ---- DOM bridge (not supported: safe stubs)
  function fakeElement(){
    var el = { id: nid('el'), tagName: 'DIV', attributes: {} };
    ['setText','setAttribute','removeAttribute','setProperty','addClass','removeClass','setCssText','setStyle','removeStyle','setDataAttribute',
     'removeDataAttribute','setInnerHTML','append','appendChild','removeChild','before','after','remove','addEventListener'].forEach(function(m){ el[m] = function(){}; });
    ['getText','getAttribute','getProperty','getStyle','getComputedStyle','getDataAttribute','getParent'].forEach(function(m){ el[m] = function(){ return Promise.resolve(null); }; });
    ['getAttributes','getDataAttributes'].forEach(function(m){ el[m] = function(){ return Promise.resolve({}); }; });
    ['hasAttribute','hasClass','hasStyle','hasDataAttribute'].forEach(function(m){ el[m] = function(){ return Promise.resolve(false); }; });
    el.getChildren = function(){ return Promise.resolve([]); }; el.query = function(){ return Promise.resolve([]); }; el.queryOne = function(){ return Promise.resolve(null); };
    return el;
  }
  var dom = {
    query: function(){ return Promise.resolve([]); }, queryOne: function(){ return Promise.resolve(null); },
    observe: function(){ return [function(){}, function(){}]; }, observeInView: function(){ return [function(){}, function(){}]; },
    createElement: function(){ return Promise.resolve(fakeElement()); }, asElement: function(){ return fakeElement(); },
    onReady: function(cb){ setTimeout(safe(cb, 'dom.onReady'), 0); }, onMainTabReady: function(cb){ setTimeout(safe(cb, 'dom.onMainTabReady'), 0); },
    viewport: { getSize: function(){ return Promise.resolve({ width: 1920, height: 1080 }); }, onResize: function(){ return function(){}; } },
    clipboard: { write: function(text){ host.emit('clipboard', { text: String(text) }); } }
  };

  // ---- cache & settings
  var cacheMap = {};
  var cache = {
    get: function(k){ var e = cacheMap[k]; if (!e) return undefined; if (e.exp && e.exp < Date.now()) { delete cacheMap[k]; return undefined; } return e.v; },
    set: function(k, v, opt){ var ttl = typeof opt === 'number' ? opt : (opt && opt.ttl) || 0; cacheMap[k] = { v: v, exp: ttl ? Date.now() + ttl : 0 }; },
    has: function(k){ return cache.get(k) !== undefined; },
    remove: function(k){ delete cacheMap[k]; }, 'delete': function(k){ delete cacheMap[k]; }, clear: function(){ cacheMap = {}; },
    size: function(){ return Object.keys(cacheMap).length; }
  };
  cache.getOrSet = cache.remember = cache.getOrLoad = function(k, loader, ttl){
    if (cache.has(k)) return cache.get(k);
    var v = loader();
    if (v && typeof v.then === 'function') return v.then(function(r){ cache.set(k, r, ttl); return r; });
    cache.set(k, v, ttl); return v;
  };
  function getPath(o, path){ if (!path) return o; return String(path).split('.').reduce(function(a, k){ return a == null ? undefined : a[k]; }, o); }
  function setPath(o, path, v){ var ks = String(path).split('.'); var last = ks.pop(); var t = ks.reduce(function(a, k){ if (a[k] == null || typeof a[k] !== 'object') a[k] = {}; return a[k]; }, o); t[last] = v; }
  var settings = {
    define: function(name, defaults){
      var key = 'settings:' + name;
      var stored = (typeof $storage !== 'undefined' && $storage.get(key)) || {};
      var data = Object.assign({}, defaults || {}, stored);
      var watchers = [];
      var api = {
        get: function(path){ return getPath(data, path); },
        set: function(a, b){ if (typeof a === 'object') Object.assign(data, a); else setPath(data, a, b); watchers.forEach(function(cb){ safe(cb, 'settings.watch')(data); }); },
        save: function(){ if (typeof $storage !== 'undefined') $storage.set(key, data); },
        reset: function(){ data = Object.assign({}, defaults || {}); api.save(); },
        watch: function(cb){ watchers.push(cb); return function(){ watchers = watchers.filter(function(x){ return x !== cb; }); }; },
        fieldRef: function(path){ var r = fieldRef(getPath(data, path)); r.onValueChange(function(v){ setPath(data, path, v); }); return r; }
      };
      return api;
    }
  };
  var jobs = {
    debounce: function(fn, ms){ var t = null; return function(){ var args = arguments; if (t) clearTimeout(t); t = setTimeout(function(){ fn.apply(null, args); }, ms || 300); }; },
    singleflight: function(key, fn){ return fn(); },
    poll: function(fn, ms){ var i = setInterval(safe(fn, 'poll'), ms || 1000); return function(){ clearInterval(i); }; },
    cancel: function(){}, cancelAll: function(){}, isRunning: function(){ return false; }
  };

  function noopObject(){ return new Proxy({}, { get: function(t, k){ if (k === 'then') return undefined; return function(){ return Promise.resolve(undefined); }; } }); }

  var ctx = {
    state: makeState, computed: computed, effect: effect, fieldRef: fieldRef,
    setTimeout: function(fn, ms){ var t = setTimeout(safe(fn, 'setTimeout'), Math.floor(ms || 0)); return function(){ clearTimeout(t); }; },
    setInterval: function(fn, ms){ var t = setInterval(safe(fn, 'setInterval'), Math.floor(ms || 0)); return function(){ clearInterval(t); }; },
    registerEventHandler: function(name, fn){ handlers[name] = fn; return function(){ delete handlers[name]; }; },
    eventHandler: function(key, fn){ var id = 'eh:' + key; handlers[id] = fn; return id; },
    fetch: function(url, opts){ return fetch(url, opts); },
    toast: {
      success: function(m){ host.toast('success', String(m)); }, error: function(m){ host.toast('error', String(m)); },
      info: function(m){ host.toast('info', String(m)); }, warning: function(m){ host.toast('warning', String(m)); },
      alert: function(m){ host.toast('warning', String(m)); }
    },
    newTray: newTray, newWebview: newWebview,
    newCommandPalette: function(){ var p = { setItems: function(){}, refresh: function(){}, open: function(){}, close: function(){}, setInput: function(){}, getInput: function(){ return ''; }, onOpen: function(){}, onClose: function(){} }; palettes.push(p); return withComponents(p); },
    newForm: function(){ return withComponents({}); },
    action: {
      newAnimePageButton: function(p){ return newAction('anime-page-button', p); },
      newMangaPageButton: function(p){ return newAction('manga-page-button', p); },
      newAnimePageDropdownItem: function(p){ return newAction('anime-page-dropdown', p); },
      newMangaPageDropdownItem: function(p){ return newAction('manga-page-dropdown', p); },
      newAnimeLibraryDropdownItem: function(p){ return newAction('anime-library-dropdown', p); },
      newMangaLibraryDropdownItem: function(p){ return newAction('manga-library-dropdown', p); },
      newMediaCardContextMenuItem: function(p){ return newAction('media-card-context', p); },
      newEpisodeCardContextMenuItem: function(p){ return newAction('episode-card-context', p); },
      newEpisodeGridItemMenuItem: function(p){ return newAction('episode-grid-menu', p); }
    },
    screen: {
      navigateTo: function(path, params){ host.emit('navigate', { path: path, searchParams: params || {} }); },
      reload: function(){ host.emit('reload', {}); },
      loadCurrent: function(){ var s = screenState; navCallbacks.forEach(function(cb){ setTimeout(function(){ safe(cb, 'screen.onNavigate')(s); }, 0); }); },
      onNavigate: function(cb){ navCallbacks.push(cb); return function(){ navCallbacks = navCallbacks.filter(function(x){ return x !== cb; }); }; },
      state: function(){ return screenState; }
    },
    dom: dom, cache: cache, settings: settings, jobs: jobs,
    anime: {
      getAnimeEntry: function(id){ try { return Promise.resolve(host.animeEntry(id)); } catch (e) { return Promise.reject(e); } },
      getAnimeMetadata: function(){ return Promise.resolve(null); },
      getEpisodeCollection: function(){ return Promise.resolve(null); },
      getEntryDownloadInfo: function(){ return Promise.resolve(null); },
      registerEntryEpisodeTab: function(){ return { unregister: function(){} }; },
      clearEpisodeMetadataCache: function(){}, clearCache: function(){}
    },
    manga: {
      getCollection: function(){ try { return Promise.resolve(host.mangaCollection()); } catch (e) { return Promise.reject(e); } },
      getMangaEntry: function(id){ try { return Promise.resolve({ mediaId: id, media: host.manga(id) }); } catch (e) { return Promise.reject(e); } },
      getChapterContainer: function(){ return Promise.resolve(null); }, getDownloadedChapters: function(){ return Promise.resolve([]); },
      refreshChapters: function(){ return Promise.resolve(); }, emptyCache: function(){ return Promise.resolve(); }, getProviders: function(){ return Promise.resolve([]); }
    },
    notification: { send: function(m){ host.toast('info', String(m)); } },
    externalPlayerLink: { open: function(url){ host.emit('open-url', { url: String(url) }); } },
    continuity: noopObject(), fillerManager: noopObject(), autoDownloader: noopObject(), autoScanner: noopObject(),
    autoSelect: noopObject(), torrentSearch: noopObject(), scanner: noopObject(), onlinestream: noopObject(), mediastream: noopObject(),
    playback: noopObject(), videoCore: noopObject(), torrentstream: noopObject(), discord: noopObject(), cron: noopObject(), chromeDP: noopObject()
  };

  // ---- globals
  g.$ui = { register: function(fn){ uiCallbacks.push(fn); } };
  g.$shared = { define: function(name, factory){ shared[name] = factory; }, use: function(name){ return shared[name] ? shared[name]() : undefined; } };
  // Plugins expect these two as plain strings.
  var appBase = { getVersion: '3.10.3', getVersionName: 'Kumo', invalidateClientQuery: function(){},
    getClientIds: function(){ return []; }, getClientPlatform: function(){ return 'denshi'; } };
  g.$app = new Proxy(appBase, { get: function(t, k){
    if (k in t) return t[k];
    if (typeof k === 'string' && k.indexOf('on') === 0) return function(fn){ (hooks[k] = hooks[k] || []).push(fn); };
    return undefined;
  } });
  if (host.hasScope('anilist')) {
    g.$anilist = {
      getAnime: function(id){ return host.anilist.getAnime(id); },
      getAnimeDetails: function(id){ return host.anilist.getAnimeDetails(id); },
      getManga: function(id){ return host.anilist.getManga(id); },
      getMangaDetails: function(id){ return host.anilist.getManga(id); },
      getAnimeCollection: function(b){ return host.anilist.collection('ANIME', !!b); },
      getRawAnimeCollection: function(b){ return host.anilist.collection('ANIME', !!b); },
      getMangaCollection: function(b){ return host.anilist.collection('MANGA', !!b); },
      getRawMangaCollection: function(b){ return host.anilist.collection('MANGA', !!b); },
      getAnimeCollectionWithRelations: function(){ return host.anilist.collection('ANIME', false); },
      refreshAnimeCollection: function(){ host.anilist.collection('ANIME', true); },
      refreshMangaCollection: function(){ host.anilist.collection('MANGA', true); },
      updateEntry: function(mediaId, status, scoreRaw, progress){ host.anilist.updateEntry(mediaId, status, scoreRaw, progress); },
      updateEntryProgress: function(mediaId, progress){ host.anilist.updateEntry(mediaId, null, null, progress); },
      updateEntryRepeat: function(){},
      deleteEntry: function(mediaId){ host.anilist.deleteEntry(mediaId); },
      addMediaToCollection: function(ids){ (ids || []).forEach(function(id){ host.anilist.updateEntry(id, 'PLANNING', null, null); }); },
      customQuery: function(body, token){ return host.anilist.customQuery(body, token || ''); },
      listAnime: function(page, search, perPage){ return host.anilist.listAnime(page || 1, search || '', perPage || 20); },
      clearCache: function(){}, getRequestProvider: function(){ return 'anilist'; }
    };
  }
  if (host.hasScope('database')) {
    g.$database = {
      anilist: {
        getToken: function(){ return host.token(); },
        getUsername: function(){ var v = host.viewer(); return v ? v.name : ''; },
        getAvatarUrl: function(){ var v = host.viewer(); return v ? v.avatar : ''; }
      },
      localFiles: { getAll: function(){ return host.localFiles(); }, findBy: function(fn){ return host.localFiles().filter(fn); }, save: function(){}, insert: function(){} },
      autoDownloaderRules: noopObject(), autoDownloaderProfiles: noopObject(), autoDownloaderItems: noopObject(),
      silencedMediaEntries: { getAllIds: function(){ return []; }, isSilenced: function(){ return false; }, setSilenced: function(){} },
      mediaFillers: noopObject()
    };
  }

  // ---- entry points called from Go
  function startUI(){
    uiCallbacks.forEach(function(fn){ fn(ctx); });
    scheduleRender();
  }

  function runHook(name, raw){
    var list = hooks[name];
    if (!list || !list.length) return;
    var data = JSON.parse(raw);
    list.forEach(function(fn){
      var e = Object.assign({}, data, { next: function(){}, preventDefault: function(){} });
      safe(fn, name)(e);
    });
  }

  function dispatch(raw){
    var evt = JSON.parse(raw);
    switch (evt.kind) {
      case 'handler': {
        var h = handlers[evt.name];
        if (h) safe(h, 'handler ' + evt.name)(evt.payload || {});
        break;
      }
      case 'field': {
        var r = fieldRefs[evt.ref];
        if (r) { r.current = evt.value; r._cbs.forEach(function(cb){ safe(cb, 'onValueChange')(evt.value); }); }
        if (evt.handler && handlers[evt.handler]) safe(handlers[evt.handler], 'onChange')({ value: evt.value });
        break;
      }
      case 'tray-open': trays.forEach(function(t){ if (t.id === evt.trayId) t._open.forEach(function(cb){ safe(cb, 'tray.onOpen')(); }); }); break;
      case 'tray-close': trays.forEach(function(t){ if (t.id === evt.trayId) t._close.forEach(function(cb){ safe(cb, 'tray.onClose')(); }); }); break;
      case 'tray-click': trays.forEach(function(t){ if (t.id === evt.trayId) t._click.forEach(function(cb){ safe(cb, 'tray.onClick')(); }); }); break;
      case 'action': actions.forEach(function(a){ if (a.id === evt.actionId) a._click.forEach(function(cb){ safe(cb, 'action.onClick')(evt.event || {}); }); }); break;
      case 'webview-message': webviews.forEach(function(w){ if (w.id === evt.webviewId) (w._on[evt.name] || []).forEach(function(cb){ safe(cb, 'webview.channel')(evt.payload); }); }); break;
      case 'navigate': {
        screenState = { pathname: evt.pathname || '/', searchParams: evt.searchParams || {} };
        navCallbacks.forEach(function(cb){ safe(cb, 'screen.onNavigate')(screenState); });
        break;
      }
    }
  }

  return { startUI: startUI, hook: runHook, dispatch: dispatch };
})
`)
