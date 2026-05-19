// Package server constructs the chi-based HTTP handler.
package server

import (
	"context"
	"encoding/json"
	"html"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/ContinuumApp/continuum-plugin-annas-archive-downloader/internal/catalog"
	"github.com/ContinuumApp/continuum-plugin-annas-archive-downloader/internal/ebookdb"
	"github.com/ContinuumApp/continuum-plugin-annas-archive-downloader/internal/runtime"
	"github.com/ContinuumApp/continuum-plugin-annas-archive-downloader/internal/store"
)

type Deps struct {
	EbookDBClient *ebookdb.Client
	Store         *store.Store
	Config        runtime.Config
}

type Server struct {
	deps Deps
}

func New(d Deps) *Server { return &Server{deps: d} }

func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Get("/admin", s.handleAdminHome)
	r.Get("/admin/", s.handleAdminHome)
	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/health", s.handleHealth)
		r.Get("/capabilities", s.handleCapabilities)
		r.Get("/admin/diagnostics", s.handleDiagnostics)
		r.Get("/admin/config", s.handleGetConfig)
		r.Patch("/admin/config", s.handleUpdateConfig)
		r.Get("/admin/test-search", s.handleTestSearch)
		if s.deps.EbookDBClient != nil {
			catalog.NewHandler(s.deps.EbookDBClient).Mount(r)
		}
	})
	return r
}

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	if s.deps.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "store not configured"})
		return
	}
	cfg, err := s.deps.Store.GetAppConfig(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	cfg.APIKey = ""
	writeJSON(w, http.StatusOK, cfg)
}

func (s *Server) handleUpdateConfig(w http.ResponseWriter, r *http.Request) {
	if s.deps.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "store not configured"})
		return
	}
	cur, err := s.deps.Store.GetAppConfig(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	var next runtime.Config
	if err := json.NewDecoder(r.Body).Decode(&next); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON body"})
		return
	}
	if next.APIKey == "" {
		next.APIKey = cur.APIKey
	}
	if err := s.deps.Store.UpdateAppConfig(r.Context(), next); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleAdminHome(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(`<!doctype html>
<html lang="en" data-theme="` + adminTheme(r) + `">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Anna's Archive Downloader</title><style>` + adminThemeCSS() + `</style></head>
<body>
<main class="shell">
<a class="back" href="/admin/plugins">&larr; Plugins</a>
<header><p class="eyebrow">Ebook request provider</p><h1>Anna's Archive Downloader</h1><p>Ebook search, request forwarding, and reconciliation for the Ebooks portal.</p></header>
<nav class="tabs" aria-label="Anna's Archive admin sections">
<button class="tab active" data-tab-target="readiness" type="button">Readiness</button>
<button class="tab" data-tab-target="config" type="button">Config</button>
<button class="tab" data-tab-target="search-lab" type="button">Search lab</button>
<button class="tab" data-tab-target="request-queue" type="button">Request queue</button>
<button class="tab" data-tab-target="guardrails" type="button">Guardrails</button>
</nav>
<section class="tab-panel active" id="readiness">
<article class="panel"><div class="panel-head"><div><h2>Setup status</h2><p class="muted">Confirms database and upstream downloader reachability before Ebooks routes requests here.</p></div><span id="ready-badge" class="badge">Loading</span></div><div id="status" class="cards muted">Loading diagnostics...</div></article>
</section>
<section class="tab-panel" id="config">
<article class="panel"><div class="panel-head"><div><h2>Plugin config</h2><p class="muted">Downloader connection and external source priority live in this plugin database.</p></div><span id="config-state" class="badge">Loading</span></div><form id="config-form" class="config-grid"><label>Base URL<input id="cfg-base-url" placeholder="https://downloads.example.com"></label><label>API key<input id="cfg-api-key" type="password" placeholder="Leave blank to keep current key"></label><label>Default cover size<input id="cfg-cover-size" placeholder="large"></label><label class="span-all">External source priority JSON<textarea id="cfg-priority" rows="5" placeholder='["libgen","zlibrary"]'></textarea></label><button type="submit">Save config</button></form><pre id="config-output" class="output">Loading config...</pre></article>
</section>
<section class="tab-panel" id="search-lab">
<article class="panel"><div class="panel-head"><div><h2>Provider test</h2><p class="muted">Run a known query before enabling request traffic; failures usually indicate mirror, auth, or upstream schema drift.</p></div></div><form id="search-form" class="row"><input id="q" value="foundation" placeholder="Search title or author" aria-label="Search query"><button type="submit">Test search</button></form><pre id="search-output" class="output">No test run yet.</pre><div class="triage-grid"><div><h3>Mirror health</h3><p>Search failures often come from upstream shape changes, blocked requests, or credential problems.</p></div><div><h3>Match quality</h3><p>Compare title, author, format, language, and identifier fields before approving a provider switch.</p></div><div><h3>Request fit</h3><p>This plugin is a request/download provider, not a full local catalog source.</p></div></div></article>
</section>
<section class="tab-panel" id="request-queue">
<article class="panel"><div class="panel-head"><div><h2>Request queue</h2><p class="muted">Use request stats to distinguish retryable dependency outages from terminal provider failures.</p></div></div><div id="queue-output" class="cards muted">Loading request stats...</div></article>
</section>
<section class="tab-panel" id="guardrails">
<article class="panel"><div class="panel-head"><div><h2>Failure triage</h2><p class="muted"><strong>Policy guardrails</strong> keep terminal failures, retries, and source-priority changes obvious.</p></div></div><div class="triage-grid">
<div><h3>Retryable</h3><p>Temporary upstream, network, or database errors can usually be retried by letting the reconciler poll again after the dependency recovers.</p></div>
<div><h3>Stuck searching</h3><p>If requests remain in searching or acknowledged states, run provider test search and confirm the upstream downloader accepts the same title/author payload.</p></div>
<div><h3>Terminal failures</h3><p>Failed and not_found rows are terminal. Re-submit from the portal after correcting metadata, source priority, or upstream credentials.</p></div>
</div></section>
</section>
<section class="panel"><h2>Operations checklist</h2><ul><li>Configure <code>database_url</code>, <code>base_url</code>, and <code>api_key</code>.</li><li>Select this plugin as an ebook request/download provider in the Ebooks portal.</li><li>Use test search before allowing users to submit requests.</li><li>Review request stats when reconciliation looks stale.</li></ul></section>
</main>
<script>
const statusEl=document.getElementById("status"), output=document.getElementById("search-output"), queueOutput=document.getElementById("queue-output"), configOutput=document.getElementById("config-output"), configState=document.getElementById("config-state");
const hostToken=new URLSearchParams(location.search).get("token")||"";
function headers(){return hostToken?{Authorization:"Bearer "+hostToken}:{}}
function badge(ok){return '<span class="badge '+(ok?'ok':'bad')+'">'+(ok?'OK':'Needs attention')+'</span>'}
function esc(v){return String(v??"").replace(/[&<>"']/g,c=>({"&":"&amp;","<":"&lt;",">":"&gt;",'"':"&quot;","'":"&#39;"}[c]))}
function activateTab(id){document.querySelectorAll(".tab").forEach(b=>b.classList.toggle("active",b.dataset.tabTarget===id));document.querySelectorAll(".tab-panel").forEach(p=>p.classList.toggle("active",p.id===id))}
document.querySelectorAll(".tab").forEach(b=>b.addEventListener("click",()=>activateTab(b.dataset.tabTarget)))
async function loadConfig(){try{const r=await fetch("./api/v1/admin/config",{headers:headers()});const d=await r.json();if(!r.ok)throw new Error(d.error||r.statusText);document.getElementById("cfg-base-url").value=d.base_url||"";document.getElementById("cfg-cover-size").value=d.default_cover_size||"large";document.getElementById("cfg-priority").value=JSON.stringify(d.external_source_priority||[],null,2);configState.textContent="Loaded";configOutput.textContent=JSON.stringify(d,null,2)}catch(e){configState.textContent="Unavailable";configOutput.textContent=String(e)}}
async function load(){try{const r=await fetch("./api/v1/admin/diagnostics",{headers:headers()});const d=await r.json();const ready=d.configured&&d.database?.ok&&d.upstream?.ok;document.getElementById("ready-badge").textContent=ready?"Ready":"Needs attention";statusEl.innerHTML='<div class="diag">'+badge(d.configured)+'<strong>Configured</strong><span>base_url, api_key, and database_url applied</span></div><div class="diag">'+badge(d.database?.ok)+'<strong>Database</strong><span>'+esc(d.database?.message)+'</span></div><div class="diag">'+badge(d.upstream?.ok)+'<strong>Upstream</strong><span>'+esc(d.upstream?.message)+'</span></div><div class="diag"><strong>Base URL</strong><span>'+esc(d.base_url||"not set")+'</span></div>';queueOutput.innerHTML='<div class="diag"><strong>Request stats</strong><span>'+esc(JSON.stringify(d.requests||{},null,2))+'</span></div><div class="diag"><strong>Provider role</strong><span>'+esc(d.role||"download_provider")+'</span></div>'}catch(e){statusEl.textContent=String(e);queueOutput.textContent=String(e)}} 
document.getElementById("config-form").addEventListener("submit",async e=>{e.preventDefault();configState.textContent="Saving";try{const body={base_url:document.getElementById("cfg-base-url").value.trim(),api_key:document.getElementById("cfg-api-key").value,default_cover_size:document.getElementById("cfg-cover-size").value.trim()||"large",external_source_priority:JSON.parse(document.getElementById("cfg-priority").value||"[]")};const r=await fetch("./api/v1/admin/config",{method:"PATCH",headers:{...headers(),"Content-Type":"application/json"},body:JSON.stringify(body)});const d=await r.json();if(!r.ok)throw new Error(d.error||r.statusText);document.getElementById("cfg-api-key").value="";configState.textContent="Saved";configOutput.textContent=JSON.stringify(d,null,2);await loadConfig()}catch(err){configState.textContent="Error";configOutput.textContent=String(err)}})
document.getElementById("search-form").addEventListener("submit",async e=>{e.preventDefault();output.textContent="Searching...";try{const q=encodeURIComponent(document.getElementById("q").value||"foundation");const r=await fetch("./api/v1/admin/test-search?q="+q,{headers:headers()});output.textContent=JSON.stringify(await r.json(),null,2)}catch(err){output.textContent=String(err)}})
load();loadConfig();
</script>
</body></html>`))
}

func adminTheme(r *http.Request) string {
	theme := r.Header.Get("X-Continuum-Theme")
	if theme == "" {
		theme = r.URL.Query().Get("theme")
	}
	if theme == "" {
		theme = "default"
	}
	return html.EscapeString(theme)
}

func adminThemeCSS() string {
	return `:root{--bg:#141417;--fg:#e8e8ec;--muted:#a1a1aa;--link:#93c5fd;--panel:#1c1c20;--border:#28282e;--ok:#22c55e;--bad:#fb7185;--input:#101014}[data-theme="cinema-light"]{--bg:#f7f3ed;--fg:#201c18;--muted:#756b60;--link:#9a3412;--panel:#fffaf3;--border:#ded1c0;--input:#fff}[data-theme="cobalt-studio"]{--bg:#101623;--fg:#eef4ff;--muted:#afc2e2;--link:#60a5fa;--panel:#172033;--border:#2d3f61;--input:#0d1422}[data-theme="oxblood-noir"]{--bg:#170b10;--fg:#fff1f4;--muted:#f0a6b7;--link:#fb7185;--panel:#241018;--border:#4a2230;--input:#12070b}[data-theme="evergreen-studio"]{--bg:#0d1712;--fg:#ecfdf3;--muted:#9bd6b4;--link:#6ee7b7;--panel:#14241b;--border:#2b4b39;--input:#08110d}*{box-sizing:border-box}body{font-family:system-ui,sans-serif;margin:0;line-height:1.5;background:var(--bg);color:var(--fg)}.shell{max-width:1120px;margin:0 auto;padding:28px}.back{display:inline-flex;margin-bottom:12px;color:var(--link);text-decoration:none}.eyebrow{color:var(--muted);text-transform:uppercase;font-size:12px;letter-spacing:.08em}h1{margin:.2rem 0}h2{font-size:16px;margin:0}.tabs{display:flex;gap:8px;flex-wrap:wrap;margin:18px 0}.tab{background:transparent;color:var(--fg);border:1px solid var(--border)}.tab.active{background:var(--link);color:#08111f}.tab-panel{display:none}.tab-panel.active{display:block}.grid,.triage-grid,.cards{display:grid;grid-template-columns:repeat(auto-fit,minmax(240px,1fr));gap:16px}.panel{border:1px solid var(--border);background:var(--panel);border-radius:8px;padding:16px;margin-top:16px}.panel-head{display:flex;align-items:flex-start;justify-content:space-between;gap:16px}.triage-grid h3{font-size:14px;margin:.2rem 0}.triage-grid p{color:var(--muted);margin:.25rem 0}.row{display:grid;grid-template-columns:minmax(0,1fr) auto}.config-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:10px;margin-top:12px}.config-grid label{display:grid;gap:6px;color:var(--muted);font-size:13px}.config-grid .span-all{grid-column:1/-1}.diag{display:grid;gap:4px;border:1px solid var(--border);border-radius:6px;background:var(--input);padding:12px}.diag strong{color:var(--fg)}.diag span{color:var(--muted);font-size:12px}textarea,input{min-width:0;background:var(--input);color:var(--fg);border:1px solid var(--border);border-radius:6px;padding:9px}textarea{font-family:ui-monospace,SFMono-Regular,Menlo,monospace;resize:vertical}button{background:var(--link);border:0;border-radius:6px;padding:9px 12px;color:#08111f;font-weight:700;cursor:pointer}.badge{display:inline-block;border:1px solid var(--border);border-radius:999px;padding:2px 8px;margin-right:6px;font-size:12px;white-space:nowrap}.ok{color:var(--ok)}.bad{color:var(--bad)}.muted{color:var(--muted)}.output{overflow:auto;max-height:340px;background:var(--input);border:1px solid var(--border);border-radius:6px;padding:10px;color:var(--fg)}code{color:var(--link)}@media(max-width:760px){.row,.panel-head,.config-grid{grid-template-columns:1fr;display:grid}}`
}

func (s *Server) handleDiagnostics(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	upstreamOK := false
	upstreamMessage := "not configured"
	if s.deps.EbookDBClient != nil {
		if err := s.deps.EbookDBClient.Ping(ctx); err != nil {
			upstreamMessage = err.Error()
		} else {
			upstreamOK = true
			upstreamMessage = "upstream reachable"
		}
	}
	dbOK := false
	dbMessage := "not configured"
	var stats any = map[string]any{}
	if s.deps.Store != nil {
		if err := s.deps.Store.Pool().Ping(ctx); err != nil {
			dbMessage = err.Error()
		} else {
			dbOK = true
			dbMessage = "database reachable"
		}
		if requestStats, err := s.deps.Store.RequestStats(ctx); err == nil {
			stats = requestStats
		}
	}
	writeJSON(w, 200, map[string]any{
		"plugin_id":  "continuum.annas-archive-downloader",
		"role":       "download_provider",
		"configured": s.deps.Config.ProviderConfigured(),
		"base_url":   s.deps.Config.BaseURL,
		"features":   []string{"external_search", "request_snapshot", "admin_diagnostics", "provider_test_search"},
		"upstream": map[string]any{
			"ok":      upstreamOK,
			"message": upstreamMessage,
		},
		"database": map[string]any{
			"ok":      dbOK,
			"message": dbMessage,
		},
		"requests": stats,
	})
}

func (s *Server) handleTestSearch(w http.ResponseWriter, r *http.Request) {
	if s.deps.EbookDBClient == nil {
		writeJSON(w, 200, map[string]any{"ok": false, "message": "not configured", "items": []any{}})
		return
	}
	query := r.URL.Query().Get("q")
	if query == "" {
		query = "foundation"
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	hits, err := s.deps.EbookDBClient.ExternalSearch(ctx, query, 5)
	if err != nil {
		writeJSON(w, 200, map[string]any{"ok": false, "message": err.Error(), "items": []any{}})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "message": "search completed", "items": hits})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
