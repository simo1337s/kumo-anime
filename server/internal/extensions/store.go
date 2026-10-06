package extensions

import (
	"encoding/json"
	"sync"

	"github.com/dop251/goja"

	"github.com/simo1337s/animetest/server/internal/db"
)

func stringifyJS(vm *goja.Runtime, v goja.Value) (json.RawMessage, bool) {
	if v == nil || goja.IsUndefined(v) {
		return nil, false
	}
	js, _ := goja.AssertFunction(vm.Get("JSON").ToObject(vm).Get("stringify"))
	s, err := js(goja.Undefined(), v)
	if err != nil || goja.IsUndefined(s) {
		return nil, false
	}
	return json.RawMessage(s.String()), true
}

func parseJS(vm *goja.Runtime, raw json.RawMessage) goja.Value {
	if raw == nil {
		return goja.Undefined()
	}
	parse, _ := goja.AssertFunction(vm.Get("JSON").ToObject(vm).Get("parse"))
	v, err := parse(goja.Undefined(), vm.ToValue(string(raw)))
	if err != nil {
		return goja.Undefined()
	}
	return v
}

// MemStore is $store: an in-memory key/value store shared by all VMs of an
// extension (values are stored as JSON, so get() always returns a copy).
type MemStore struct {
	mu       sync.Mutex
	data     map[string]json.RawMessage
	watchers map[string][]*storeWatcher
	nextID   int
}

type storeWatcher struct {
	id int
	rt *Runtime
	fn goja.Callable
}

func NewMemStore() *MemStore {
	return &MemStore{data: map[string]json.RawMessage{}, watchers: map[string][]*storeWatcher{}}
}

func (s *MemStore) set(key string, raw json.RawMessage) {
	s.mu.Lock()
	s.data[key] = raw
	ws := append([]*storeWatcher(nil), s.watchers[key]...)
	s.mu.Unlock()
	for _, w := range ws {
		w := w
		w.rt.RunOnLoop(func(vm *goja.Runtime) {
			if _, err := w.fn(goja.Undefined(), parseJS(vm, raw)); err != nil {
				w.rt.log("error", "$store.watch: "+jsError(err).Error())
			}
		})
	}
}

// Unwatch removes every watcher registered by a runtime.
func (s *MemStore) Unwatch(r *Runtime) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, ws := range s.watchers {
		var keep []*storeWatcher
		for _, w := range ws {
			if w.rt != r {
				keep = append(keep, w)
			}
		}
		s.watchers[k] = keep
	}
}

func (s *MemStore) Bind(r *Runtime, vm *goja.Runtime) *goja.Object {
	o := vm.NewObject()
	get := func(key string) goja.Value {
		s.mu.Lock()
		raw, ok := s.data[key]
		s.mu.Unlock()
		if !ok {
			return goja.Undefined()
		}
		return parseJS(vm, raw)
	}
	all := func() goja.Value {
		s.mu.Lock()
		m := make(map[string]json.RawMessage, len(s.data))
		for k, v := range s.data {
			m[k] = v
		}
		s.mu.Unlock()
		raw, _ := json.Marshal(m)
		return parseJS(vm, raw)
	}
	values := func() goja.Value {
		s.mu.Lock()
		arr := make([]json.RawMessage, 0, len(s.data))
		for _, v := range s.data {
			arr = append(arr, v)
		}
		s.mu.Unlock()
		raw, _ := json.Marshal(arr)
		return parseJS(vm, raw)
	}
	_ = o.Set("set", func(key string, v goja.Value) {
		if raw, ok := stringifyJS(vm, v); ok {
			s.set(key, raw)
		} else {
			s.mu.Lock()
			delete(s.data, key)
			s.mu.Unlock()
		}
	})
	_ = o.Set("get", get)
	_ = o.Set("getUnsafe", get)
	_ = o.Set("has", func(key string) bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		_, ok := s.data[key]
		return ok
	})
	_ = o.Set("remove", func(key string) {
		s.mu.Lock()
		delete(s.data, key)
		s.mu.Unlock()
	})
	_ = o.Set("removeAll", func() {
		s.mu.Lock()
		s.data = map[string]json.RawMessage{}
		s.mu.Unlock()
	})
	_ = o.Set("reset", func() {
		s.mu.Lock()
		s.data = map[string]json.RawMessage{}
		s.mu.Unlock()
	})
	_ = o.Set("length", func() int {
		s.mu.Lock()
		defer s.mu.Unlock()
		return len(s.data)
	})
	_ = o.Set("getAll", all)
	_ = o.Set("getAllUnsafe", all)
	_ = o.Set("values", values)
	_ = o.Set("valuesUnsafe", values)
	_ = o.Set("getOrSet", func(key string, fn goja.Callable) goja.Value {
		s.mu.Lock()
		raw, ok := s.data[key]
		s.mu.Unlock()
		if ok {
			return parseJS(vm, raw)
		}
		v, err := fn(goja.Undefined())
		if err != nil {
			panic(err)
		}
		if raw, ok := stringifyJS(vm, v); ok {
			s.set(key, raw)
		}
		return v
	})
	_ = o.Set("setIfLessThanLimit", func(key string, v goja.Value, limit int) bool {
		s.mu.Lock()
		n := len(s.data)
		_, exists := s.data[key]
		s.mu.Unlock()
		if n >= limit && !exists {
			return false
		}
		if raw, ok := stringifyJS(vm, v); ok {
			s.set(key, raw)
		}
		return true
	})
	_ = o.Set("marshalJSON", func(v goja.Value) string {
		raw, _ := stringifyJS(vm, v)
		return string(raw)
	})
	_ = o.Set("unmarshalJSON", func(data string) {
		var m map[string]json.RawMessage
		if json.Unmarshal([]byte(data), &m) == nil {
			for k, v := range m {
				s.set(k, v)
			}
		}
	})
	_ = o.Set("watch", func(key string, fn goja.Callable) goja.Value {
		s.mu.Lock()
		s.nextID++
		w := &storeWatcher{id: s.nextID, rt: r, fn: fn}
		s.watchers[key] = append(s.watchers[key], w)
		s.mu.Unlock()
		return vm.ToValue(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			ws := s.watchers[key]
			for i, x := range ws {
				if x.id == w.id {
					s.watchers[key] = append(ws[:i], ws[i+1:]...)
					break
				}
			}
		})
	})
	return o
}

// Storage is $storage: persistent per-extension key/value storage.
type Storage struct {
	db    *db.DB
	extID string
}

func NewStorage(d *db.DB, extID string) *Storage { return &Storage{db: d, extID: extID} }

func (s *Storage) Get(key string) (json.RawMessage, bool) {
	var raw string
	err := s.db.QueryRow(`SELECT value FROM extension_storage WHERE ext_id = ? AND key = ?`, s.extID, key).Scan(&raw)
	if err != nil {
		return nil, false
	}
	return json.RawMessage(raw), true
}

func (s *Storage) Set(key string, raw json.RawMessage) error {
	_, err := s.db.Write(`INSERT INTO extension_storage(ext_id, key, value) VALUES(?, ?, ?)
		ON CONFLICT(ext_id, key) DO UPDATE SET value = excluded.value`, s.extID, key, string(raw))
	return err
}

func (s *Storage) Bind(vm *goja.Runtime) *goja.Object {
	o := vm.NewObject()
	get := func(key string) goja.Value {
		raw, ok := s.Get(key)
		if !ok {
			return goja.Undefined()
		}
		return parseJS(vm, raw)
	}
	_ = o.Set("get", get)
	_ = o.Set("getUnsafe", get)
	_ = o.Set("set", func(key string, v goja.Value) error {
		raw, ok := stringifyJS(vm, v)
		if !ok {
			_, err := s.db.Write(`DELETE FROM extension_storage WHERE ext_id = ? AND key = ?`, s.extID, key)
			return err
		}
		return s.Set(key, raw)
	})
	_ = o.Set("remove", func(key string) error {
		_, err := s.db.Write(`DELETE FROM extension_storage WHERE ext_id = ? AND key = ?`, s.extID, key)
		return err
	})
	clear := func() error {
		_, err := s.db.Write(`DELETE FROM extension_storage WHERE ext_id = ?`, s.extID)
		return err
	}
	_ = o.Set("drop", clear)
	_ = o.Set("clear", clear)
	_ = o.Set("has", func(key string) bool { _, ok := s.Get(key); return ok })
	_ = o.Set("keys", func() []string {
		rows, err := s.db.Query(`SELECT key FROM extension_storage WHERE ext_id = ?`, s.extID)
		if err != nil {
			return nil
		}
		defer rows.Close()
		out := []string{}
		for rows.Next() {
			var k string
			if rows.Scan(&k) == nil {
				out = append(out, k)
			}
		}
		return out
	})
	_ = o.Set("watch", func(string, goja.Value) goja.Value { return vm.ToValue(func() {}) })
	return o
}
