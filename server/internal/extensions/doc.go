package extensions

import (
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/dop251/goja"
)

// installDoc binds LoadDoc / Doc (goquery-backed, Seanime compatible).
func installDoc(vm *goja.Runtime) {
	_ = vm.Set("LoadDoc", func(html string) goja.Value {
		doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
		if err != nil {
			panic(vm.NewGoError(err))
		}
		return vm.ToValue(func(call goja.FunctionCall) goja.Value {
			sel := call.Argument(0)
			if obj, ok := sel.(*goja.Object); ok {
				if s, ok := obj.Get("__sel").Export().(*goquery.Selection); ok {
					return wrapSelection(vm, s)
				}
			}
			return wrapSelection(vm, doc.Find(sel.String()))
		})
	})
	docCtor := func(call goja.ConstructorCall) *goja.Object {
		doc, err := goquery.NewDocumentFromReader(strings.NewReader(call.Argument(0).String()))
		if err != nil {
			panic(vm.NewGoError(err))
		}
		return wrapSelection(vm, doc.Selection).ToObject(vm)
	}
	_ = vm.Set("Doc", docCtor)
}

func optSel(call goja.FunctionCall, i int) (string, bool) {
	a := call.Argument(i)
	if a == nil || goja.IsUndefined(a) || goja.IsNull(a) {
		return "", false
	}
	return a.String(), true
}

// selPredicate converts a selector string or (i, el) => bool callback.
func selPredicate(vm *goja.Runtime, v goja.Value) (string, func(int, *goquery.Selection) bool) {
	if fn, ok := goja.AssertFunction(v); ok {
		return "", func(i int, s *goquery.Selection) bool {
			res, err := fn(goja.Undefined(), vm.ToValue(i), wrapSelection(vm, s))
			if err != nil {
				panic(err)
			}
			return res.ToBoolean()
		}
	}
	return v.String(), nil
}

func wrapSelection(vm *goja.Runtime, s *goquery.Selection) goja.Value {
	o := vm.NewObject()
	wrap := func(x *goquery.Selection) goja.Value { return wrapSelection(vm, x) }
	_ = o.Set("__sel", s)
	_ = o.Set("length", func() int { return s.Length() })
	_ = o.Set("html", func() goja.Value {
		h, err := s.Html()
		if err != nil || s.Length() == 0 {
			return goja.Null()
		}
		return vm.ToValue(h)
	})
	_ = o.Set("outerHtml", func() string { h, _ := goquery.OuterHtml(s); return h })
	_ = o.Set("text", func() string { return s.Text() })
	_ = o.Set("attr", func(name string) goja.Value {
		if v, ok := s.Attr(name); ok {
			return vm.ToValue(v)
		}
		return goja.Undefined()
	})
	_ = o.Set("attrs", func() map[string]string {
		out := map[string]string{}
		if s.Length() > 0 {
			for _, a := range s.Get(0).Attr {
				out[a.Key] = a.Val
			}
		}
		return out
	})
	_ = o.Set("data", func(call goja.FunctionCall) goja.Value {
		if name, ok := optSel(call, 0); ok {
			if v, ok := s.Attr("data-" + name); ok {
				return vm.ToValue(v)
			}
			return goja.Undefined()
		}
		out := map[string]string{}
		if s.Length() > 0 {
			for _, a := range s.Get(0).Attr {
				if strings.HasPrefix(a.Key, "data-") {
					out[a.Key] = a.Val
				}
			}
		}
		return vm.ToValue(out)
	})
	_ = o.Set("find", func(sel string) goja.Value { return wrap(s.Find(sel)) })
	_ = o.Set("children", func(call goja.FunctionCall) goja.Value {
		if sel, ok := optSel(call, 0); ok {
			return wrap(s.ChildrenFiltered(sel))
		}
		return wrap(s.Children())
	})
	_ = o.Set("contents", func() goja.Value { return wrap(s.Contents()) })
	_ = o.Set("contentsFiltered", func(sel string) goja.Value { return wrap(s.ContentsFiltered(sel)) })
	_ = o.Set("parent", func(call goja.FunctionCall) goja.Value {
		if sel, ok := optSel(call, 0); ok {
			return wrap(s.ParentFiltered(sel))
		}
		return wrap(s.Parent())
	})
	_ = o.Set("parents", func(call goja.FunctionCall) goja.Value {
		if sel, ok := optSel(call, 0); ok {
			return wrap(s.ParentsFiltered(sel))
		}
		return wrap(s.Parents())
	})
	_ = o.Set("parentsUntil", func(call goja.FunctionCall) goja.Value {
		sel, _ := optSel(call, 0)
		if until, ok := optSel(call, 1); ok {
			return wrap(s.ParentsFilteredUntil(sel, until))
		}
		return wrap(s.ParentsUntil(sel))
	})
	_ = o.Set("closest", func(call goja.FunctionCall) goja.Value {
		sel, _ := optSel(call, 0)
		return wrap(s.Closest(sel))
	})
	type nav struct {
		name     string
		all      func() *goquery.Selection
		filtered func(string) *goquery.Selection
	}
	for _, n := range []nav{
		{"next", s.Next, s.NextFiltered},
		{"nextAll", s.NextAll, s.NextAllFiltered},
		{"prev", s.Prev, s.PrevFiltered},
		{"prevAll", s.PrevAll, s.PrevAllFiltered},
		{"siblings", s.Siblings, s.SiblingsFiltered},
	} {
		n := n
		_ = o.Set(n.name, func(call goja.FunctionCall) goja.Value {
			if sel, ok := optSel(call, 0); ok {
				return wrap(n.filtered(sel))
			}
			return wrap(n.all())
		})
	}
	_ = o.Set("nextUntil", func(call goja.FunctionCall) goja.Value {
		sel, _ := optSel(call, 0)
		if until, ok := optSel(call, 1); ok {
			return wrap(s.NextFilteredUntil(sel, until))
		}
		return wrap(s.NextUntil(sel))
	})
	_ = o.Set("prevUntil", func(call goja.FunctionCall) goja.Value {
		sel, _ := optSel(call, 0)
		if until, ok := optSel(call, 1); ok {
			return wrap(s.PrevFilteredUntil(sel, until))
		}
		return wrap(s.PrevUntil(sel))
	})
	_ = o.Set("first", func() goja.Value { return wrap(s.First()) })
	_ = o.Set("last", func() goja.Value { return wrap(s.Last()) })
	_ = o.Set("eq", func(i int) goja.Value { return wrap(s.Eq(i)) })
	_ = o.Set("end", func() goja.Value { return wrap(s.End()) })
	_ = o.Set("filter", func(v goja.Value) goja.Value {
		sel, fn := selPredicate(vm, v)
		if fn != nil {
			return wrap(s.FilterFunction(fn))
		}
		return wrap(s.Filter(sel))
	})
	_ = o.Set("not", func(v goja.Value) goja.Value {
		sel, fn := selPredicate(vm, v)
		if fn != nil {
			return wrap(s.NotFunction(fn))
		}
		return wrap(s.Not(sel))
	})
	_ = o.Set("is", func(v goja.Value) bool {
		sel, fn := selPredicate(vm, v)
		if fn != nil {
			return s.IsFunction(fn)
		}
		return s.Is(sel)
	})
	_ = o.Set("has", func(sel string) goja.Value { return wrap(s.Has(sel)) })
	_ = o.Set("each", func(fn goja.Callable) {
		s.EachWithBreak(func(i int, x *goquery.Selection) bool {
			res, err := fn(goja.Undefined(), vm.ToValue(i), wrap(x))
			if err != nil {
				panic(err)
			}
			// Returning false breaks like jQuery.
			if b, ok := res.Export().(bool); ok && !b {
				return false
			}
			return true
		})
	})
	_ = o.Set("map", func(fn goja.Callable) goja.Value {
		out := make([]any, 0, s.Length())
		s.Each(func(i int, x *goquery.Selection) {
			res, err := fn(goja.Undefined(), vm.ToValue(i), wrap(x))
			if err != nil {
				panic(err)
			}
			out = append(out, res)
		})
		return vm.NewArray(out...)
	})
	return o
}
