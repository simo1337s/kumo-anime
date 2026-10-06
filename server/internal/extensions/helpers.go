package extensions

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var (
	reSeason = regexp.MustCompile(`(?i)\b(?:season|s)\s*0*(\d{1,2})\b|\b(\d{1,2})(?:st|nd|rd|th)\s+season\b|(\d{1,2})期`)
	rePart   = regexp.MustCompile(`(?i)\b(?:part|cour)\s*(\d{1,2})\b|\b(\d{1,2})(?:st|nd|rd|th)\s+(?:part|cour)\b`)
	reYear   = regexp.MustCompile(`\b(19\d{2}|20\d{2})\b`)
)

func extractNum(re *regexp.Regexp, s string) int {
	m := re.FindStringSubmatch(s)
	if m == nil {
		return -1
	}
	for _, g := range m[1:] {
		if g != "" {
			n, err := strconv.Atoi(g)
			if err == nil {
				return n
			}
		}
	}
	return -1
}

func ordinalStr(n int) string {
	switch {
	case n%100 >= 11 && n%100 <= 13:
		return fmt.Sprintf("%dth", n)
	case n%10 == 1:
		return fmt.Sprintf("%dst", n)
	case n%10 == 2:
		return fmt.Sprintf("%dnd", n)
	case n%10 == 3:
		return fmt.Sprintf("%drd", n)
	}
	return fmt.Sprintf("%dth", n)
}

func containsStr(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}

// jsPrelude contains globals that are easiest to express in JavaScript.
const jsPrelude = `
(function(){
  var g = globalThis;
  g.$clone = function(v){ return v === undefined ? undefined : JSON.parse(JSON.stringify(v)); };
  g.$replace = function(target, src){
    if (!target || typeof target !== 'object') return;
    Object.keys(target).forEach(function(k){ delete target[k]; });
    Object.assign(target, src);
  };
  g.$mutable = function(v){ return v; };
  g.$await = function(p){ return p; };
  g.$unmarshal = function(data, dst){ var v = typeof data === 'string' ? JSON.parse(data) : data; $replace(dst, v); return dst; };
  g.$arrayOf = function(){ return []; };
  g.$toPointer = function(v){ return v; };
  g.$debug = { enabled: false, log: console.log, info: console.info, warn: console.warn, error: console.error,
    debug: console.debug, inspect: console.log, mark: function(){}, time: function(){}, timeEnd: function(){}, clear: function(){} };

  function FormData(){ this.__kumoFormData = true; this._entries = []; }
  FormData.prototype.append = function(k, v){ this._entries.push([String(k), v]); };
  FormData.prototype.set = function(k, v){ this.delete(k); this.append(k, v); };
  FormData.prototype.delete = function(k){ this._entries = this._entries.filter(function(e){ return e[0] !== String(k); }); };
  FormData.prototype.get = function(k){ var e = this._entries.find(function(e){ return e[0] === String(k); }); return e ? e[1] : null; };
  FormData.prototype.getAll = function(k){ return this._entries.filter(function(e){ return e[0] === String(k); }).map(function(e){ return e[1]; }); };
  FormData.prototype.has = function(k){ return this._entries.some(function(e){ return e[0] === String(k); }); };
  FormData.prototype.entries = function(){ return this._entries.slice(); };
  FormData.prototype.keys = function(){ return this._entries.map(function(e){ return e[0]; }); };
  FormData.prototype.values = function(){ return this._entries.map(function(e){ return e[1]; }); };
  g.FormData = FormData;

  var notSupported = function(){ return Promise.reject(new Error('ChromeDP is not supported in Kumo')); };
  g.ChromeDP = { scrape: notSupported, screenshot: notSupported, evaluate: notSupported, newBrowser: notSupported };
  g.$torrentUtils = { getMagnetLinkFromTorrentData: function(){ throw new Error('not supported'); } };
  g.$waitGroup = function(){ return { add: function(){}, done: function(){}, wait: function(){} }; };
  g.$unsafeGoroutine = function(fn){ setTimeout(fn, 0); };
  if (typeof g.structuredClone === 'undefined') g.structuredClone = g.$clone;
})();
`
