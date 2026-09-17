package main

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

const managementPath = "/plugins/quota-window-activator/status"

func managementRegistration(request pluginapi.ManagementRegistrationRequest) pluginapi.ManagementRegistrationResponse {
	path := managementPath
	if id := pluginIDFromResourceBase(request.ResourceBasePath); id != "" {
		path = "/plugins/" + id + "/status"
	}
	return pluginapi.ManagementRegistrationResponse{Routes: []pluginapi.ManagementRoute{{Method: http.MethodGet, Path: path, Description: "Read quota window activator status."}}, Resources: []pluginapi.ResourceRoute{{Path: "/status", Menu: "额度窗口激活器", Description: "Configure the activator and inspect quota-window state."}}}
}
func handleManagement(raw []byte) ([]byte, error) {
	var request pluginapi.ManagementRequest
	if len(raw) > 0 {
		if e := json.Unmarshal(raw, &request); e != nil {
			return nil, e
		}
	}
	if isManagementStatusRequest(request) {
		globalMu.Lock()
		runtime := globalRuntime
		globalMu.Unlock()
		if runtime == nil {
			return okEnvelope(pluginapi.ManagementResponse{StatusCode: http.StatusServiceUnavailable, Headers: http.Header{"Content-Type": []string{"application/json"}}, Body: []byte(`{"error":"runtime unavailable"}`)})
		}
		body, e := json.Marshal(runtime.status())
		if e != nil {
			return nil, e
		}
		return okEnvelope(pluginapi.ManagementResponse{StatusCode: http.StatusOK, Headers: http.Header{"Content-Type": []string{"application/json"}}, Body: body})
	}
	return okEnvelope(pluginapi.ManagementResponse{StatusCode: http.StatusOK, Headers: http.Header{"Content-Type": []string{"text/html; charset=utf-8"}, "Cache-Control": []string{"no-store"}}, Body: []byte(statusPage)})
}

func pluginIDFromResourceBase(resourceBase string) string {
	resourceBase = strings.Trim(strings.TrimSpace(resourceBase), "/")
	if resourceBase == "" {
		return ""
	}
	parts := strings.Split(resourceBase, "/")
	return strings.TrimSpace(parts[len(parts)-1])
}

func isManagementStatusRequest(request pluginapi.ManagementRequest) bool {
	if request.Method != "" && !strings.EqualFold(request.Method, http.MethodGet) {
		return false
	}
	path := strings.TrimRight(strings.TrimSpace(request.Path), "/")
	if path == managementPath || path == "/v0/management"+managementPath {
		return true
	}
	return strings.Contains(path, "/v0/management/plugins/") && strings.HasSuffix(path, "/status")
}

const statusPage = `<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width">
<title>额度窗口激活器</title>
<style>
:root{color-scheme:light;font:14px system-ui,-apple-system,"Segoe UI",sans-serif;color:#172033;background:#f4f7fb}*{box-sizing:border-box}body{margin:0}.wrap{max-width:1480px;margin:auto;padding:28px}.title{display:flex;align-items:flex-start;justify-content:space-between;gap:18px;margin-bottom:20px}h1{margin:0 0 5px;font-size:25px}h2{margin:0 0 16px;font-size:18px}.muted{color:#64748b}.card{background:#fff;border:1px solid #dce3ed;border-radius:12px;padding:20px;margin-bottom:18px;box-shadow:0 2px 8px #15213a0a}.bar{display:flex;align-items:center;gap:9px;flex-wrap:wrap}.key{width:min(360px,100%)}input,select,textarea,button{font:inherit}input,select,textarea{border:1px solid #cbd5e1;border-radius:7px;padding:8px 10px;background:#fff;color:#172033}textarea{width:100%;min-height:88px;resize:vertical}button{border:0;border-radius:7px;padding:9px 14px;background:#2563eb;color:#fff;cursor:pointer}button.secondary{background:#475569}button:disabled{opacity:.55;cursor:wait}.notice{min-height:20px;color:#475569}.notice.error{color:#b91c1c}.notice.ok{color:#047857}.summary{display:grid;grid-template-columns:repeat(5,minmax(120px,1fr));gap:10px;margin-top:16px}.metric{padding:11px;border-radius:8px;background:#f8fafc}.metric b{display:block;margin-top:4px;word-break:break-word}.grid{display:grid;grid-template-columns:repeat(2,minmax(260px,1fr));gap:14px 20px}.field label{display:block;font-weight:600;margin-bottom:6px}.field input:not([type=checkbox]),.field select{width:100%}.check{display:flex;align-items:center;gap:8px;font-weight:600}.hint{display:block;color:#64748b;font-size:12px;margin-top:5px}.provider-grid{display:grid;grid-template-columns:repeat(3,minmax(210px,1fr));gap:10px}.provider{border:1px solid #dce3ed;border-radius:8px;padding:11px}.provider .mode{margin-top:9px;width:100%}.actions{display:flex;align-items:center;gap:10px;margin-top:18px}.table-wrap{overflow:auto}table{border-collapse:collapse;width:100%;min-width:1180px}th,td{padding:9px 10px;border-bottom:1px solid #e2e8f0;text-align:left;white-space:nowrap}th{font-size:12px;color:#475569;background:#f8fafc;position:sticky;top:0}td.result{white-space:normal;min-width:220px}code{font-size:12px}.pill{display:inline-block;padding:3px 7px;border-radius:999px;background:#e2e8f0}.danger{background:#fee2e2;color:#991b1b}.safe{background:#dcfce7;color:#166534}@media(max-width:850px){.wrap{padding:16px}.title{display:block}.grid,.provider-grid,.summary{grid-template-columns:1fr}.key{width:100%}}
</style></head><body><main class="wrap">
<div class="title"><div><h1>额度窗口激活器</h1><div class="muted">配置 lazy reset 激活策略，并查看每个 credential + bucket 的运行状态。</div></div><span class="pill">v0.2.2</span></div>
<section class="card"><h2>连接 CPA Management API</h2><p class="muted">管理密钥只保留在当前页面内存中，不会写入插件状态或浏览器存储。</p><div class="bar"><input class="key" id="key" type="password" autocomplete="off" placeholder="CPA Management key"><button id="load" type="button">加载配置和状态</button><span id="notice" class="notice"></span></div><div class="summary"><div class="metric">插件状态<b id="sumEnabled">—</b></div><div class="metric">运行模式<b id="sumMode">—</b></div><div class="metric">窗口数<b id="sumWindows">—</b></div><div class="metric">最后检查<b id="sumRun">—</b></div><div class="metric">最后错误<b id="sumError">—</b></div></div></section>
<section class="card"><h2>运行配置</h2><form id="configForm"><div class="grid"><div class="field"><label class="check"><input id="enabled" type="checkbox">启用后台激活调度</label><span class="hint">控制插件自己的后台调度。</span></div><div class="field"><label class="check"><input id="dryRun" type="checkbox">Dry-run</label><span class="hint">只检测并记录 would activate，不发送模型请求。首次安装建议保持开启。</span></div><div class="field"><label for="observationInterval">检查间隔</label><input id="observationInterval" required value="30m"><span class="hint">例如 30m。</span></div><div class="field"><label for="resetGracePeriod">Reset 宽限期</label><input id="resetGracePeriod" required value="60s"><span class="hint">旧窗口到期后等待多久再 precheck。</span></div><div class="field"><label for="verifyDelay">验证延迟</label><input id="verifyDelay" required value="3s"><span class="hint">activation 后等待配额传播的时间。</span></div><div class="field"><label for="suppressionDuration">抑制窗口</label><input id="suppressionDuration" required value="10m"><span class="hint">发送结果未知时只 verify、不重发。</span></div><div class="field"><label for="requestTimeout">请求超时</label><input id="requestTimeout" required value="30s"></div><div class="field"><label for="maxRetries">最大重试次数</label><input id="maxRetries" type="number" min="1" step="1" required value="5"><span class="hint">只影响控制面重读，不突破每周期一次 send fence。</span></div><div class="field"><label for="maxConcurrency">最大并发数</label><input id="maxConcurrency" type="number" min="1" step="1" required value="2"></div><div class="field"><label for="stateDir">状态目录</label><input id="stateDir" placeholder="留空使用操作系统默认目录"><span class="hint">保存 runtime-state.json 与 WAL。</span></div></div>
<h2 style="margin-top:24px">自动激活 Provider</h2><div id="providers" class="provider-grid"></div><p class="muted">未列出的 provider 不会被后台轮询；确认存在 lazy reset 并实现 credential-bound adapter 后才会加入。</p>
<div class="field" style="margin-top:18px"><label for="disabledCredentials">禁用的 Credential Auth ID</label><textarea id="disabledCredentials" placeholder="每行一个 Auth ID"></textarea><span class="hint">这些 credential 不会被读取 quota，也不会发送 activation。</span></div>
<div class="actions"><button id="save" type="submit">保存配置</button><span id="saveNotice" class="notice"></span></div></form></section>
<section class="card"><div class="title"><div><h2>Quota Window 状态</h2><span class="muted">按 credential + quota bucket 独立跟踪。</span></div><button id="refresh" class="secondary" type="button">刷新状态</button></div><div class="table-wrap"><table><thead><tr><th>Provider</th><th>Credential</th><th>Bucket</th><th>Model Family</th><th>Used</th><th>Reset At</th><th>Window</th><th>Probe State</th><th>Last Probe</th><th>Next Check</th><th>Last Activation</th><th>Result</th></tr></thead><tbody id="rows"><tr><td colspan="12" class="muted">请输入 Management key 并加载。</td></tr></tbody></table></div></section>
</main>
<script>
const resourceMarker='/v0/resource/plugins/';
const markerIndex=location.pathname.indexOf(resourceMarker);
const appBase=markerIndex>=0?location.pathname.slice(0,markerIndex):'';
const resourceTail=markerIndex>=0?location.pathname.slice(markerIndex+resourceMarker.length):'';
const pluginID=decodeURIComponent(resourceTail.split('/')[0]||'quota-window-activator');
const managementBase=appBase+'/v0/management/plugins/'+encodeURIComponent(pluginID);
const statusPath=managementBase+'/status';
const configPath=managementBase+'/config';
const providerSpecs=[['codex','Codex / ChatGPT OAuth','支持主 5h 与长周期 credential-bound activation。'],['antigravity','Antigravity','支持 Gemini 与 Claude/GPT 两组 5h/weekly bucket。']];
const defaults={enabled:true,dry_run:true,observation_interval:'30m',reset_grace_period:'60s',verify_delay:'3s',suppression_duration:'10m',request_timeout:'30s',max_retries:5,max_concurrency:2};
const $=id=>document.getElementById(id),rows=$('rows'),notice=$('notice'),saveNotice=$('saveNotice'),key=$('key');
let currentConfig={};
function auth(){const value=key.value.trim();if(!value)throw new Error('请输入 Management key');return value.toLowerCase().startsWith('bearer ')?value:'Bearer '+value}
async function api(path,options){const opts=options||{};opts.cache='no-store';opts.headers=Object.assign({},opts.headers||{},{Authorization:auth()});if(opts.body)opts.headers['Content-Type']='application/json';const r=await fetch(path,opts);let body=null;const text=await r.text();if(text){try{body=JSON.parse(text)}catch(_){body=text}}if(!r.ok){const detail=body&&body.message?body.message:(body&&body.error?body.error:'HTTP '+r.status);throw new Error(path+'：'+detail)}return body}
function setNotice(el,text,kind){el.textContent=text;el.className='notice '+(kind||'')}
function renderProviderInputs(){const root=$('providers');root.innerHTML='';providerSpecs.forEach(spec=>{const id=spec[0],label=spec[1],hint=spec[2],box=document.createElement('div');box.className='provider';box.innerHTML='<label class="check"><input type="checkbox" id="provider-'+e(id)+'">'+e(label)+'</label><span class="hint">'+e(hint)+'</span>';root.appendChild(box)})}
function applyConfig(raw){currentConfig=raw&&typeof raw==='object'?JSON.parse(JSON.stringify(raw)):{};const c=Object.assign({},defaults,currentConfig);$('enabled').checked=c.enabled!==false;$('dryRun').checked=c.dry_run!==false;$('observationInterval').value=c.observation_interval||defaults.observation_interval;$('resetGracePeriod').value=c.reset_grace_period||defaults.reset_grace_period;$('verifyDelay').value=c.verify_delay||defaults.verify_delay;$('suppressionDuration').value=c.suppression_duration||defaults.suppression_duration;$('requestTimeout').value=c.request_timeout||defaults.request_timeout;$('maxRetries').value=c.max_retries||defaults.max_retries;$('maxConcurrency').value=c.max_concurrency||defaults.max_concurrency;$('stateDir').value=c.state_dir||'';$('disabledCredentials').value=Array.isArray(c.disabled_credentials)?c.disabled_credentials.join('\n'):'';const providers=c.providers||{};providerSpecs.forEach(spec=>{const id=spec[0],p=providers[id];$('provider-'+id).checked=!p||(p.enabled!==false&&p.mode!=='observe')})}
function duration(name){const value=$(name).value.trim();if(!/^([0-9]+(\.[0-9]+)?(ns|us|µs|ms|s|m|h))+$/.test(value))throw new Error($(name).previousElementSibling.textContent+' 格式无效');return value}
function positiveInt(name){const value=Number($(name).value);if(!Number.isInteger(value)||value<1)throw new Error($(name).previousElementSibling.textContent+' 必须是正整数');return value}
function collectConfig(){const c=JSON.parse(JSON.stringify(currentConfig||{}));c.enabled=$('enabled').checked;c.dry_run=$('dryRun').checked;c.observation_interval=duration('observationInterval');c.reset_grace_period=duration('resetGracePeriod');c.verify_delay=duration('verifyDelay');c.suppression_duration=duration('suppressionDuration');c.request_timeout=duration('requestTimeout');c.max_retries=positiveInt('maxRetries');c.max_concurrency=positiveInt('maxConcurrency');const stateDir=$('stateDir').value.trim();if(stateDir)c.state_dir=stateDir;else delete c.state_dir;if(!c.providers||typeof c.providers!=='object'||Array.isArray(c.providers))c.providers={};providerSpecs.forEach(spec=>{const id=spec[0],prior=c.providers[id]&&typeof c.providers[id]==='object'?c.providers[id]:{};c.providers[id]=Object.assign({},prior,{enabled:$('provider-'+id).checked,mode:'activate_if_lazy'})});c.disabled_credentials=$('disabledCredentials').value.split(/\r?\n|,/).map(x=>x.trim()).filter((x,i,a)=>x&&a.indexOf(x)===i);return c}
function e(x){return String(x==null?'':x).replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]))}
function dt(x){if(!x||String(x).startsWith('0001-'))return '—';const d=new Date(x);return isNaN(d)?e(x):d.toLocaleString()}
function durationText(ns){const n=Number(ns);if(!Number.isFinite(n)||n<=0)return '—';const s=n/1e9;if(s%86400===0)return s/86400+'d';if(s%3600===0)return s/3600+'h';if(s%60===0)return s/60+'m';return s+'s'}
function used(x){return x==null?'—':Number(x).toFixed(1)+'%'}
function renderStatus(s){const list=s&&Array.isArray(s.windows)?s.windows:[];$('sumEnabled').textContent=s&&s.enabled?'运行中':'已停止';$('sumMode').textContent=s&&s.dry_run?'Dry-run':'允许激活';$('sumMode').className=s&&s.dry_run?'safe':'danger';$('sumWindows').textContent=String(list.length);$('sumRun').textContent=dt(s&&s.last_run);$('sumError').textContent=s&&s.last_error?s.last_error:'无';if(!list.length){rows.innerHTML='<tr><td colspan="12" class="muted">尚未观察到 quota window。等待下一次观察，或检查 provider credential。</td></tr>';return}rows.innerHTML=list.map(w=>'<tr><td>'+e(w.provider)+'</td><td>'+e(w.auth_id)+'</td><td><code>'+e(w.bucket_id)+'</code></td><td>'+e(w.model_family||w.latest&&w.latest.model_family)+'</td><td>'+used(w.latest&&w.latest.used_percent)+'</td><td>'+dt(w.latest&&w.latest.reset_at)+'</td><td>'+durationText(w.latest&&w.latest.window_duration)+'</td><td><span class="pill">'+e(w.state)+'</span></td><td>'+dt(w.last_probe)+'</td><td>'+dt(w.next_check)+'</td><td>'+dt(w.last_activation)+'</td><td class="result">'+e(w.last_result)+'</td></tr>').join('')}
async function loadAll(){setNotice(notice,'加载中…','');try{const values=await Promise.all([api(configPath),api(statusPath)]);applyConfig(values[0]);renderStatus(values[1]);setNotice(notice,'已更新 '+new Date().toLocaleTimeString(),'ok')}catch(x){setNotice(notice,'加载失败：'+x.message,'error')}}
async function refreshStatus(){setNotice(notice,'刷新状态…','');try{renderStatus(await api(statusPath));setNotice(notice,'已更新 '+new Date().toLocaleTimeString(),'ok')}catch(x){setNotice(notice,'刷新失败：'+x.message,'error')}}
async function saveConfig(event){event.preventDefault();setNotice(saveNotice,'','');$('save').disabled=true;try{const next=collectConfig();if(currentConfig.dry_run!==false&&next.dry_run===false&&!confirm('关闭 Dry-run 后，插件可在确认 lazy reset 时发送最小真实模型请求。确定继续吗？'))return;setNotice(saveNotice,'保存中…','');await api(configPath,{method:'PUT',body:JSON.stringify(next)});currentConfig=next;setNotice(saveNotice,'保存成功；CPA 正在重新加载插件配置。','ok');setTimeout(loadAll,800)}catch(x){setNotice(saveNotice,'保存失败：'+x.message,'error')}finally{$('save').disabled=false}}
renderProviderInputs();$('load').onclick=loadAll;$('refresh').onclick=refreshStatus;$('configForm').onsubmit=saveConfig;key.onkeydown=x=>{if(x.key==='Enter'){x.preventDefault();loadAll()}};
</script></body></html>`
