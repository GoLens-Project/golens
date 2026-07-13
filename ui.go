package golens

import (
	"bytes"
	"encoding/json"
	"html/template"
	"net/http"
	"strconv"
)

// MountUI registers the HTMX dashboard routes on the given mux. The dashboard
// is server-side rendered; HTMX polls the data endpoint at the configured
// interval. Passing nil for mux uses http.DefaultServeMux.
//
// If declarative admin auth is configured (UI.Auth), every dashboard route is
// wrapped with HTTP Basic Auth before mounting. With auth disabled the raw
// handlers are mounted unprotected — advanced users can instead skip MountUI
// and wrap DashboardHandler / DataHandler in their own security middleware.
func (r *Registry) MountUI(mux *http.ServeMux) {
	if mux == nil {
		mux = http.DefaultServeMux
	}
	if !r.cfg.UI.Enabled {
		return
	}

	// wrap applies the built-in basic-auth decorator only when configured.
	wrap := func(h http.HandlerFunc) http.Handler {
		if r.auth.active() {
			return r.basicAuth(h)
		}
		return h
	}

	mux.Handle("/metrics", wrap(r.DashboardHandler))
	mux.Handle("/metrics/data", wrap(r.DataHandler))
	mux.Handle("/metrics/data/", wrap(r.DataHandler))
	mux.Handle("/metrics/endpoints", wrap(func(w http.ResponseWriter, req *http.Request) {
		writeJSON(w, r.EndpointLatency())
	}))
}

// DashboardHandler is the raw, unprotected handler that renders the main
// dashboard HTML. Exposed so advanced users can mount it behind their own
// authentication middleware (JWT, sessions, OAuth, ...).
func (r *Registry) DashboardHandler(w http.ResponseWriter, req *http.Request) {
	r.metricsPageHandler().ServeHTTP(w, req)
}

// DataHandler is the raw, unprotected handler for /metrics/data (JSON snapshot
// or HTMX fragment). Exposed for custom-middleware wrapping.
func (r *Registry) DataHandler(w http.ResponseWriter, req *http.Request) {
	r.metricsDataHandler().ServeHTTP(w, req)
}

// EndpointsHTTPHandler returns per-endpoint latency snapshots (p50/p95/p99) as
// JSON, for the endpoint-percentile chart. Returns nil if the UI is disabled.
func (r *Registry) EndpointsHTTPHandler() http.Handler {
	if !r.cfg.UI.Enabled {
		return nil
	}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		writeJSON(w, r.EndpointLatency())
	})
}

// MetricsHTTPHandler returns the dashboard page handler, for routers that need
// to mount routes individually (e.g. Gin). Returns nil if the UI is disabled.
func (r *Registry) MetricsHTTPHandler() http.Handler {
	if !r.cfg.UI.Enabled {
		return nil
	}
	return r.metricsPageHandler()
}

// MetricsDataHTTPHandler returns the metrics data handler (JSON or HTMX
// fragment) for individual route mounting. Returns nil if the UI is disabled.
func (r *Registry) MetricsDataHTTPHandler() http.Handler {
	if !r.cfg.UI.Enabled {
		return nil
	}
	return r.metricsDataHandler()
}

// metricsPageHandler renders the full dashboard shell. The card grid is
// refreshed by HTMX polling /metrics/data.
func (r *Registry) metricsPageHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		intervalMs := r.cfg.UI.PollInterval.Milliseconds()
		if intervalMs <= 0 {
			intervalMs = 5000
		}
		data := map[string]interface{}{
			"PollMs":   intervalMs,
			"PollSec":  intervalMs / 1000,
			"Cards":    buildCards(r.Snapshots()),
			"Interval": r.cfg.UI.PollInterval.String(),
		}
		// Render to a buffer first so a template error never produces a
		// half-written response (which would force a superfluous WriteHeader).
		var buf bytes.Buffer
		if err := dashboardTpl.Execute(&buf, data); err != nil {
			http.Error(w, "dashboard render error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(buf.Bytes())
	})
}

// metricsDataHandler returns either a full JSON snapshot (for HTMX fragments
// or programmatic clients) or an HTML fragment depending on Accept header.
func (r *Registry) metricsDataHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		name := req.URL.Query().Get("metric")
		if name != "" {
			if s, ok := r.Snapshot(name); ok {
				writeJSON(w, s)
				return
			}
			http.NotFound(w, req)
			return
		}
		if wantsJSON(req) {
			writeJSON(w, r.Snapshots())
			return
		}
		// HTMX HTML fragment (the card grid).
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = chartsTpl.Execute(w, map[string]interface{}{"Cards": buildCards(r.Snapshots())})
	})
}

func wantsJSON(req *http.Request) bool {
	for _, a := range []string{req.Header.Get("Accept"), req.URL.Query().Get("format")} {
		if a == "json" || a == "application/json" {
			return true
		}
	}
	return false
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

// --- card rendering model --------------------------------------------------

// metricCard is the UI-facing projection of a MetricSnapshot. It carries
// pre-formatted strings and per-type styling so the templates stay logic-free.
type metricCard struct {
	Snapshot     MetricSnapshot
	Badge        string // COUNTER / GAUGE / HISTOGRAM
	BadgeClass   string // tailwind classes for the badge
	BarClass     string // tailwind classes for histogram bars
	DisplayValue string // formatted numeric value
	Unit         string // value unit label
	Bars         []histBar
}

type histBar struct {
	HeightPct int    // 0..100 relative to the busiest bucket
	Label     string // tooltip text, e.g. "≤0.005s: 50"
	Axis      string // axis label, e.g. "0.005" or "+Inf"
}

func buildCards(snaps []MetricSnapshot) []metricCard {
	cards := make([]metricCard, 0, len(snaps))
	for _, s := range snaps {
		c := metricCard{Snapshot: s}
		switch s.Type {
		case "counter":
			c.Badge = "COUNTER"
			c.BadgeClass = "bg-emerald-500/10 text-emerald-400 border-emerald-500/20"
			c.BarClass = "bg-emerald-500 hover:bg-emerald-400"
			c.DisplayValue = fmtInt(s.Value)
		case "gauge":
			c.Badge = "GAUGE"
			c.BadgeClass = "bg-sky-500/10 text-sky-400 border-sky-500/20"
			c.BarClass = "bg-sky-500 hover:bg-sky-400"
			c.DisplayValue = fmtFloat(s.Value)
		case "histogram":
			c.Badge = "HISTOGRAM"
			c.BadgeClass = "bg-purple-500/10 text-purple-400 border-purple-500/20"
			c.BarClass = "bg-purple-500 hover:bg-purple-400"
			c.DisplayValue = fmtInt(float64(s.Count))
			c.Unit = "samples"
			c.Bars = buildBars(s.Buckets)
		default:
			c.Badge = "UNKNOWN"
			c.BadgeClass = "bg-slate-500/10 text-slate-400 border-slate-500/20"
			c.DisplayValue = fmtFloat(s.Value)
		}
		cards = append(cards, c)
	}
	return cards
}

// buildBars turns histogram buckets into render-ready bars. The busiest bucket
// is 100% height; empty buckets get a 2% sliver so the axis stays legible.
func buildBars(buckets []BucketSnapshot) []histBar {
	var max int64
	for _, b := range buckets {
		if b.Count > max {
			max = b.Count
		}
	}
	bars := make([]histBar, 0, len(buckets))
	for _, b := range buckets {
		h := 2
		if max > 0 && b.Count > 0 {
			h = int(b.Count * 100 / max)
			if h < 2 {
				h = 2
			}
		}
		var label, axis string
		if b.Overflow {
			label = "+Inf: " + strconv.FormatInt(b.Count, 10)
			axis = "+Inf"
		} else {
			s := fmtBound(b.UpperBound)
			label = "≤" + s + "s: " + strconv.FormatInt(b.Count, 10)
			axis = s
		}
		bars = append(bars, histBar{HeightPct: h, Label: label, Axis: axis})
	}
	return bars
}

func fmtInt(v float64) string { return strconv.FormatFloat(v, 'f', 0, 64) }
func fmtFloat(v float64) string {
	if v == float64(int64(v)) {
		return strconv.FormatFloat(v, 'f', 0, 64)
	}
	return strconv.FormatFloat(v, 'f', 2, 64)
}

// fmtBound formats a histogram bucket boundary with up to 3 decimals and no
// trailing zeros, so 0.005 and 0.01 don't collide on the axis.
func fmtBound(v float64) string {
	s := strconv.FormatFloat(v, 'f', 3, 64)
	for len(s) > 0 && s[len(s)-1] == '0' {
		s = s[:len(s)-1]
	}
	if len(s) > 0 && s[len(s)-1] == '.' {
		s = s[:len(s)-1]
	}
	return s
}

// formatFloat is retained for template/test reuse.
func formatFloat(f float64) string { return fmtFloat(f) }

var tmplFuncs = template.FuncMap{
	"fmt": fmtFloat,
}

const dashboardSrc = `<!DOCTYPE html>
<html lang="en" x-data="{}">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>GoLens Metrics</title>
<script src="https://cdn.tailwindcss.com"></script>
<script>tailwind.config={theme:{extend:{fontFamily:{sans:['Inter','system-ui','sans-serif'],mono:['JetBrains Mono','ui-monospace','SFMono-Regular','Menlo','monospace']}}}}</script>
<link rel="preconnect" href="https://fonts.googleapis.com">
<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
<link href="https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600;700&family=JetBrains+Mono:wght@400;500;600&display=swap" rel="stylesheet">
<script src="https://unpkg.com/htmx.org@1.9.12"></script>
<script defer src="https://unpkg.com/alpinejs@3.14.1/dist/cdn.min.js"></script>
<script>
  // Apply the saved theme before paint to avoid a flash of the wrong theme.
  (function(){
    var t = 'dark';
    try { t = localStorage.getItem('golens.theme') || 'dark'; } catch(e){}
    var c = document.documentElement.classList;
    c.add(t === 'light' ? 'light' : 'dark');
    c.remove(t === 'light' ? 'dark' : 'light');
  })();
</script>
<style>
  [x-cloak]{display:none}
  ::-webkit-scrollbar{height:8px;width:8px}
  ::-webkit-scrollbar-thumb{background:#334155;border-radius:4px}
  /* Light theme: remap the dark slate palette to light surfaces/text.
     Accent colors (emerald/amber/rose/sky/purple) read fine on both. */
  html.light{color-scheme:light}
  html.light body{background:#ffffff;color:#0f172a}
  html.light .bg-slate-950{background-color:#ffffff !important}
  html.light .bg-slate-950\/80{background-color:rgba(255,255,255,.8) !important}
  html.light .bg-slate-900{background-color:#f8fafc !important}
  html.light .bg-slate-800{background-color:#e2e8f0 !important}
  html.light .bg-slate-800\/80{background-color:rgba(226,232,240,.9) !important}
  html.light .hover\:bg-slate-800:hover{background-color:#e2e8f0 !important}
  html.light .hover\:bg-rose-500\/80:hover{background-color:rgba(244,63,94,.85) !important}
  html.light .hover\:bg-slate-700{border-color:#cbd5e1 !important}
  html.light .border-slate-800{border-color:#e2e8f0 !important}
  html.light .border-slate-800\/80{border-color:rgba(226,232,240,.8) !important}
  html.light .border-slate-700{border-color:#cbd5e1 !important}
  html.light .text-white{color:#0f172a !important}
  html.light .text-slate-100{color:#0f172a !important}
  html.light .text-slate-200{color:#1e293b !important}
  html.light .text-slate-300{color:#334155 !important}
  html.light .text-slate-400{color:#475569 !important}
  html.light .text-slate-500{color:#64748b !important}
  html.light .text-slate-600{color:#94a3b8 !important}
  html.light ::-webkit-scrollbar-thumb{background:#cbd5e1}
</style>
</head>
<body class="bg-slate-950 min-h-screen text-slate-100 font-sans antialiased">

<!-- Header -->
<header class="border-b border-slate-800/80 bg-slate-950/80 backdrop-blur sticky top-0 z-20">
  <div class="max-w-7xl mx-auto px-6 py-4 flex items-center gap-4 flex-wrap">
    <div class="flex items-center gap-2 mr-auto">
      <span class="inline-block w-2.5 h-2.5 rounded-full bg-emerald-400 animate-pulse"></span>
      <h1 class="text-lg font-semibold tracking-tight">GoLens</h1>
      <span class="text-xs text-slate-500 font-mono">discovery dashboard</span>
    </div>
    <input id="search" type="text" placeholder="Search metrics..."
           class="text-sm font-mono px-3 py-1.5 rounded-lg bg-slate-900 border border-slate-800 text-slate-200 placeholder-slate-600 focus:outline-none focus:border-slate-600 w-56"
           oninput="applyFilter()">
    <button onclick="document.body.dataset.add='1'"
            class="text-sm px-3 py-1.5 rounded-lg border border-slate-800 bg-slate-900 hover:bg-slate-800 transition">+ Add Chart</button>
    <label class="text-xs text-slate-500 font-mono flex items-center gap-1.5 cursor-pointer select-none">
      <input type="checkbox" class="accent-slate-500"
             onchange="document.body.dataset.paused=this.checked?'1':''"> pause
    </label>
    <!-- Cooldown ring: depletes over the poll interval, resets on each HTMX refresh -->
    <div class="relative w-9 h-9" title="next refresh">
      <svg class="w-9 h-9 -rotate-90" viewBox="0 0 36 36">
        <circle cx="18" cy="18" r="14" fill="none" stroke="#1e293b" stroke-width="3"></circle>
        <circle id="cooldown-ring" cx="18" cy="18" r="14" fill="none" stroke="#34d399"
                stroke-width="3" stroke-linecap="round"></circle>
      </svg>
      <span id="cooldown-label" class="absolute inset-0 flex items-center justify-center text-[10px] font-mono text-slate-400">{{.PollSec}}</span>
    </div>
    <!-- Theme toggle -->
    <button id="theme-toggle" title="toggle theme"
            onclick="toggleTheme()"
            class="w-9 h-9 rounded-lg border border-slate-800 bg-slate-900 hover:bg-slate-800 text-slate-300 text-base flex items-center justify-center transition"></button>
  </div>
</header>

<main class="max-w-7xl mx-auto px-6 py-6">
  <!-- My Charts: pinned metrics, persisted in localStorage, drag-to-reorder,
       kept live by cloning the matching server-rendered card from #charts. -->
  <section x-show="$store.ui.charts.length" x-cloak
           x-data="{ drag: null,
                     start(i){ this.drag = i; },
                     drop(i){ if(this.drag===null || this.drag===i){ this.drag=null; return; }
                              const c = Alpine.store('ui').charts;
                              const m = c.splice(this.drag, 1)[0];
                              c.splice(i, 0, m);
                              saveCharts(c);
                              this.drag = null;
                              this.$nextTick(() => renderPinned()); } }"
           x-effect="$store.ui.charts.length; renderPinned()"
           class="mb-8">
    <div class="flex items-center gap-2 mb-3">
      <h2 class="text-xs font-mono uppercase tracking-wider text-slate-500">My Charts</h2>
      <span class="text-[10px] font-mono text-slate-600" x-text="'(' + $store.ui.charts.length + ')'"></span>
      <span class="text-[10px] font-mono text-slate-600">· drag to reorder</span>
    </div>
    <div id="pinned" class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-5">
      <template x-for="(name, idx) in $store.ui.charts" :key="name">
        <div draggable="true"
             @dragstart="start(idx)"
             @dragover.prevent
             @drop="drop(idx)"
             @dragend="drag=null"
             :class="drag===idx ? 'opacity-40 ring-2 ring-emerald-500/60' : ''"
             class="relative bg-slate-900 border border-slate-800 rounded-xl overflow-hidden cursor-grab active:cursor-grabbing">
          <button title="remove" @click="$store.ui.remove(name)"
                  class="absolute top-2 right-2 z-10 w-6 h-6 rounded-full bg-slate-800/80 hover:bg-rose-500/80 text-slate-300 text-xs flex items-center justify-center">×</button>
          <div class="pin-body" :data-pin="name"></div>
        </div>
      </template>
    </div>
  </section>

  <h2 class="text-xs font-mono uppercase tracking-wider text-slate-500 mb-3">All Metrics</h2>
  <div id="charts"
       hx-get="/metrics/data"
       hx-trigger="every {{.PollMs}}ms [!document.body.dataset.paused]"
       hx-swap="innerHTML">
    {{template "cards" .}}
  </div>

  <!-- Endpoint latency: p50 / p95 / p99 per endpoint, polled as JSON,
       drag-to-reorder with persisted order. -->
  <section x-data="endpointLatency({{.PollMs}})" class="mt-8">
    <div class="flex items-center gap-2 mb-3">
      <h2 class="text-xs font-mono uppercase tracking-wider text-slate-500">Endpoint Latency</h2>
      <span class="text-[10px] font-mono text-slate-600">· drag to reorder</span>
    </div>
    <div x-show="endpoints.length === 0" class="text-xs text-slate-600 font-mono">no endpoint traffic yet.</div>
    <div class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-5">
      <template x-for="(e, idx) in endpoints" :key="e.method + ' ' + e.path">
        <div draggable="true"
             @dragstart="start(idx)" @dragover.prevent @drop="drop(idx)" @dragend="drag=null"
             :class="drag===idx ? 'opacity-40 ring-2 ring-emerald-500/60' : ''"
             class="bg-slate-900 border border-slate-800 rounded-xl p-5 shadow-sm flex flex-col cursor-grab active:cursor-grabbing">
          <div class="flex items-center justify-between mb-2 gap-2">
            <span class="text-[10px] font-mono px-2 py-0.5 rounded-full border bg-sky-500/10 text-sky-400 border-sky-500/20" x-text="e.method"></span>
            <span class="text-xs text-slate-400 font-mono truncate" x-text="e.path"></span>
          </div>
          <div class="text-[11px] text-slate-500 font-mono mb-3"><span x-text="e.count"></span> requests · avg <span x-text="fmt(e.avg)"></span>s</div>
          <div class="flex items-end justify-between h-24 gap-4 border-b border-slate-800 pb-1">
            <template x-for="p in [{k:'p50',v:e.p50,c:'bg-emerald-500'},{k:'p95',v:e.p95,c:'bg-amber-500'},{k:'p99',v:e.p99,c:'bg-rose-500'}]" :key="p.k">
              <div class="flex-1 flex flex-col items-center justify-end h-full group relative">
                <div :class="p.c" class="w-full rounded-t transition-all" :style="'height:'+h(p.v)+'%'"></div>
                <span class="absolute -top-5 text-[10px] font-mono text-slate-300 opacity-0 group-hover:opacity-100" x-text="fmt(p.v)+'s'"></span>
              </div>
            </template>
          </div>
          <div class="flex justify-between text-[10px] font-mono text-slate-500 mt-1">
            <span class="text-emerald-400">p50</span><span class="text-amber-400">p95</span><span class="text-rose-400">p99</span>
          </div>
        </div>
      </template>
    </div>
  </section>
</main>

<!-- Add-chart modal (Alpine): lists all currently-available metrics by default,
     filtered live by the search box; refreshes whenever the modal opens. -->
<div x-data="{
        get list(){
          this.$store.ui.add;                       // refresh on open
          var q = (this.$store.ui.name||'').toLowerCase();
          return metricNames().filter(function(n){ return n.toLowerCase().indexOf(q) >= 0; });
        }
     }"
     x-show="$store.ui.add" x-cloak
     class="fixed inset-0 z-30 flex items-center justify-center bg-black/60" @keydown.escape.window="$store.ui.add=false">
  <div class="bg-slate-900 border border-slate-800 rounded-xl p-5 w-full max-w-md shadow-xl">
    <h3 class="text-sm font-semibold mb-3">Add chart from metric</h3>
    <input type="text" placeholder="search metrics..." x-model="$store.ui.name" autofocus
           @keydown.enter="addChart()"
           class="w-full text-sm font-mono px-3 py-2 rounded-lg bg-slate-950 border border-slate-800 text-slate-200 placeholder-slate-600 focus:outline-none focus:border-slate-600">
    <div class="mt-2 max-h-64 overflow-y-auto rounded-lg border border-slate-800 bg-slate-950">
      <template x-for="m in list" :key="m">
        <button type="button" @click="$store.ui.name=m; addChart()"
                class="block w-full text-left text-sm font-mono px-3 py-2 text-slate-300 hover:bg-slate-800 transition" x-text="m"></button>
      </template>
      <div x-show="list.length === 0" class="px-3 py-4 text-center text-xs text-slate-600 font-mono">no matching metrics</div>
    </div>
    <div class="flex justify-end gap-2 mt-4">
      <button @click="$store.ui.add=false" class="text-sm px-3 py-1.5 rounded-lg border border-slate-800 hover:bg-slate-800">Cancel</button>
      <button @click="addChart()" class="text-sm px-3 py-1.5 rounded-lg bg-emerald-500/90 hover:bg-emerald-500 text-slate-950 font-medium">Add</button>
    </div>
  </div>
</div>

<script>
  function applyTheme(t){
    var c = document.documentElement.classList;
    c.toggle('light', t === 'light');
    c.toggle('dark', t !== 'light');
    try { localStorage.setItem('golens.theme', t); } catch(e){}
    var btn = document.getElementById('theme-toggle');
    if (btn) btn.textContent = (t === 'light') ? '☀️' : '🌙';
  }
  function toggleTheme(){
    var light = document.documentElement.classList.contains('light');
    applyTheme(light ? 'dark' : 'light');
  }
  // Sync the toggle button's icon once the DOM is ready.
  document.addEventListener('DOMContentLoaded', function(){
    var t = 'dark'; try { t = localStorage.getItem('golens.theme') || 'dark'; } catch(e){}
    applyTheme(t);
  });

  function loadCharts(){ try { return JSON.parse(localStorage.getItem('golens.charts') || '[]'); } catch(e){ return []; } }
  function saveCharts(c){ try { localStorage.setItem('golens.charts', JSON.stringify(c)); } catch(e){} }

  document.addEventListener('alpine:init', () => {
    Alpine.store('ui', {
      add: false,
      name: '',
      charts: loadCharts(),
      has(n){ return this.charts.indexOf(n) >= 0; },
      add(n){ if (n && !this.has(n)) { this.charts.push(n); saveCharts(this.charts); } },
      remove(n){ this.charts = this.charts.filter(x => x !== n); saveCharts(this.charts); }
    });
  });
  // open modal from the header button — always start from a clean state
  const mo = new MutationObserver(() => {
    if (document.body.dataset.add === '1') {
      const ui = Alpine.store('ui');
      ui.name = '';        // clear any previous search/selection
      ui.add = true;       // open
      delete document.body.dataset.add;
    }
  });
  mo.observe(document.body, { attributes: true, attributeFilter: ['data-add'] });

  // Add the searched/selected metric as a pinned chart (no navigation).
  function addChart(){
    const n = Alpine.store('ui').name;
    Alpine.store('ui').add = false;
    if (n) Alpine.store('ui').add(n);
    Alpine.store('ui').name = '';
  }
  function loadOrder(){ try { return JSON.parse(localStorage.getItem('golens.order') || '[]'); } catch(e){ return []; } }
  function saveOrder(o){ try { localStorage.setItem('golens.order', JSON.stringify(o)); } catch(e){} }

  function metricNames(){
    return Array.from(document.querySelectorAll('#charts > [data-metric]')).map(el => el.dataset.metric);
  }
  function cssEsc(s){ return (window.CSS && CSS.escape) ? CSS.escape(s) : s.replace(/["\\]/g, '\\$&'); }
  // Clone each pinned metric's server-rendered card from #charts into its slot.
  function renderPinned(){
    document.querySelectorAll('#pinned [data-pin]').forEach(body => {
      const name = body.getAttribute('data-pin');
      const card = document.querySelector('#charts [data-metric="' + cssEsc(name) + '"]');
      if (card) { body.innerHTML = card.innerHTML; }
      else { body.innerHTML = '<div class="p-5 text-xs text-slate-600 font-mono">metric unavailable</div>'; }
    });
  }
  function applyFilter(){
    const q = (document.getElementById('search').value || '').toLowerCase();
    document.querySelectorAll('#charts > [data-metric]').forEach(el => {
      el.style.display = (!q || el.dataset.metric.toLowerCase().indexOf(q) >= 0) ? '' : 'none';
    });
  }
  // sortCharts reorders the All Metrics cards to match the persisted order
  // (unknown/new metrics appended at the end). Called after every HTMX swap so
  // the server's order doesn't clobber the user's custom arrangement.
  function sortCharts(){
    const grid = document.getElementById('charts');
    if (!grid) return;
    const order = loadOrder();
    const cards = Array.from(grid.querySelectorAll(':scope > [data-metric]'));
    if (cards.length <= 1) return;
    const idx = name => { const i = order.indexOf(name); return i < 0 ? Infinity : i; };
    cards.sort((a, b) => idx(a.dataset.metric) - idx(b.dataset.metric));
    cards.forEach(c => grid.appendChild(c));
  }

  // Drag-and-drop for the All Metrics grid. Uses event delegation on the
  // persistent #charts container so listeners survive HTMX innerHTML swaps.
  let dragCard = null;
  function initMetricsDnD(){
    const grid = document.getElementById('charts');
    if (!grid) return;
    grid.addEventListener('dragstart', e => {
      const card = e.target.closest('[data-metric]');
      if (!card) return;
      dragCard = card;
      card.classList.add('opacity-40', 'ring-2', 'ring-emerald-500/60');
      e.dataTransfer.effectAllowed = 'move';
    });
    grid.addEventListener('dragend', e => {
      const card = e.target.closest('[data-metric]');
      if (card) { card.classList.remove('opacity-40', 'ring-2', 'ring-emerald-500/60'); }
      dragCard = null;
    });
    grid.addEventListener('dragover', e => {
      if (!dragCard) return;
      const target = e.target.closest('[data-metric]');
      if (!target || target === dragCard) return;
      e.preventDefault();
      const r = target.getBoundingClientRect();
      const after = (e.clientY - r.top) > r.height / 2;
      if (after) target.after(dragCard); else target.before(dragCard);
    });
    grid.addEventListener('drop', e => {
      if (!dragCard) return;
      e.preventDefault();
      saveOrder(metricNames());   // persist the new arrangement
    });
  }

  // After each HTMX swap: restore custom order, reapply search filter, refresh pins.
  document.body.addEventListener('htmx:afterSwap', () => { sortCharts(); applyFilter(); renderPinned(); });

  function loadEpOrder(){ try { return JSON.parse(localStorage.getItem('golens.eporder') || '[]'); } catch(e){ return []; } }
  function saveEpOrder(o){ try { localStorage.setItem('golens.eporder', JSON.stringify(o)); } catch(e){} }

  // Per-endpoint latency chart: polls /metrics/endpoints (JSON), renders
  // p50/p95/p99 bars per endpoint, drag-to-reorder with persisted order.
  function endpointLatency(pollMs){
    return {
      raw: [],
      bump: 0,
      drag: null,
      init(){ this.load(); setInterval(() => this.load(), pollMs); },
      get endpoints(){
        this.bump; // reactive trigger for manual reorders
        const ord = loadEpOrder();
        const key = e => e.method + ' ' + e.path;
        const idx = e => { const i = ord.indexOf(key(e)); return i < 0 ? Infinity : i; };
        return this.raw.slice().sort((a, b) => idx(a) - idx(b));
      },
      get max(){
        const m = Math.max.apply(null, this.raw.map(e => Math.max(e.p50, e.p95, e.p99)).concat([0.001]));
        return m > 0 ? m : 0.001;
      },
      load(){
        fetch('/metrics/endpoints').then(r => r.json()).then(d => { this.raw = d || []; }).catch(() => {});
      },
      start(i){ this.drag = i; },
      drop(i){
        if (this.drag === null || this.drag === i) { this.drag = null; return; }
        const keys = this.endpoints.map(e => e.method + ' ' + e.path);
        const moved = keys.splice(this.drag, 1)[0];
        keys.splice(i, 0, moved);
        saveEpOrder(keys);
        this.drag = null;
        this.bump++;
      },
      h(v){ return Math.max(2, Math.round((v / this.max) * 100)); },
      fmt(v){ if (v >= 1) return v.toFixed(2); if (v >= 0.01) return v.toFixed(3); return v.toFixed(4); }
    };
  }

  // Boot: apply persisted All Metrics order + wire up its drag-and-drop.
  document.addEventListener('DOMContentLoaded', () => { sortCharts(); applyFilter(); initMetricsDnD(); });

  // Cooldown ring: depletes over the poll interval, shows remaining seconds,
  // resets on each HTMX refresh, and freezes while paused.
  (function(){
    const pollMs = {{.PollMs}};
    const ring = document.getElementById('cooldown-ring');
    const label = document.getElementById('cooldown-label');
    if (!ring || !label || !pollMs) return;
    const r = 14;
    const circ = 2 * Math.PI * r;
    ring.style.strokeDasharray = circ;
    ring.style.strokeDashoffset = 0;
    let start = performance.now();
    let pausedAt = 0;
    function frame(now){
      if (document.body.dataset.paused) {
        if (!pausedAt) { pausedAt = now; label.textContent = 'II'; }
      } else {
        if (pausedAt) { start += now - pausedAt; pausedAt = 0; }
        const remaining = pollMs - (now - start);
        const frac = Math.max(0, Math.min(1, remaining / pollMs)); // 1 → 0
        ring.style.strokeDashoffset = circ * (1 - frac);
        label.textContent = Math.max(0, Math.ceil(remaining / 1000));
      }
      requestAnimationFrame(frame);
    }
    requestAnimationFrame(frame);
    document.body.addEventListener('htmx:afterRequest', () => { start = performance.now(); });
  })();
</script>
</body>
</html>`

const cardsSrc = `{{define "cards"}}
{{if not .Cards}}
<div class="text-sm text-slate-500 font-mono py-12 text-center">No metrics recorded yet.</div>
{{end}}
<div class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-5">
{{range .Cards}}
{{$c := .}}
{{$hist := eq .Snapshot.Type "histogram"}}
<div data-metric="{{.Snapshot.Name}}" draggable="true"
     class="bg-slate-900 border border-slate-800 rounded-xl p-5 shadow-sm hover:border-slate-700 transition flex flex-col justify-between cursor-grab active:cursor-grabbing {{if $hist}}md:col-span-2{{end}}">
  <div>
    <div class="flex items-center justify-between mb-2 gap-2">
      <span class="text-[10px] font-mono font-semibold px-2 py-0.5 rounded-full border {{.BadgeClass}}">{{.Badge}}</span>
      <span class="text-xs text-slate-500 font-mono truncate">{{.Snapshot.Name}}</span>
    </div>
    <p class="text-sm text-slate-400">{{or .Snapshot.Description "—"}}</p>
  </div>

  {{if $hist}}
    <!-- histogram: CSS flexbox bar chart -->
    <div class="flex items-end justify-between h-28 gap-1 border-b border-slate-800 mt-4 pb-1">
      {{range .Bars}}
      <div class="w-full rounded-t group relative cursor-pointer transition-all {{$c.BarClass}}" style="height:{{.HeightPct}}%">
        <div class="opacity-0 group-hover:opacity-100 absolute -top-7 left-1/2 -translate-x-1/2 bg-slate-800 text-[10px] px-1.5 py-0.5 rounded font-mono whitespace-nowrap z-10 transition-opacity">{{.Label}}</div>
      </div>
      {{end}}
    </div>
    <div class="flex justify-between text-[10px] font-mono text-slate-600 mt-1">
      {{range .Bars}}<span class="truncate">{{.Axis}}</span>{{end}}
    </div>
    <div class="flex justify-between text-[11px] font-mono text-slate-400 mt-2">
      <span>count <span class="text-slate-200">{{.Snapshot.Count}}</span></span>
      <span>sum <span class="text-slate-200">{{fmt .Snapshot.Sum}}s</span></span>
      <span>avg <span class="text-slate-200">{{fmt .Snapshot.Avg}}s</span></span>
    </div>
  {{else}}
    <!-- counter / gauge: hardware-display value -->
    <div class="mt-4 flex items-baseline">
      <span class="text-4xl font-semibold tracking-tight text-white font-mono">{{.DisplayValue}}</span>
      {{if .Unit}}<span class="text-xs text-slate-500 ml-1.5 font-mono">{{.Unit}}</span>{{end}}
    </div>
    {{if .Snapshot.LabelValues}}
    <div class="mt-3 flex flex-wrap gap-1">
      {{range .Snapshot.LabelValues}}<span class="text-[10px] font-mono px-1.5 py-0.5 bg-slate-800 text-slate-400 rounded">{{.Name}}={{.Value}}</span>{{end}}
    </div>
    {{end}}
  {{end}}
</div>
{{end}}
</div>
{{end}}`

var (
	dashboardTpl = template.Must(template.New("dashboard").Funcs(tmplFuncs).Parse(dashboardSrc + cardsSrc))
	chartsTpl    = template.Must(template.New("cards").Funcs(tmplFuncs).Parse(cardsSrc))
)
