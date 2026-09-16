package main

import (
	"encoding/json"
	"net/http"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

const managementPath = "/plugins/quota-window-activator/status"

func managementRegistration() pluginapi.ManagementRegistrationResponse {
	return pluginapi.ManagementRegistrationResponse{Routes: []pluginapi.ManagementRoute{{Method: http.MethodGet, Path: managementPath, Description: "Read quota window activator status."}}, Resources: []pluginapi.ResourceRoute{{Path: "/status", Menu: "额度窗口激活器", Description: "Observe quota windows and activation state."}}}
}
func handleManagement(raw []byte) ([]byte, error) {
	var request pluginapi.ManagementRequest
	if len(raw) > 0 {
		if e := json.Unmarshal(raw, &request); e != nil {
			return nil, e
		}
	}
	if request.Path == managementPath {
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

const statusPage = `<!doctype html>
<html lang="zh-CN"><meta charset="utf-8"><meta name="viewport" content="width=device-width">
<title>额度窗口激活器</title>
<style>body{font:14px system-ui;margin:2rem;color:#172033}table{border-collapse:collapse;width:100%}th,td{padding:.55rem;border-bottom:1px solid #dde3ec;text-align:left}code{font-size:12px}.muted{color:#64748b}.bar{display:flex;gap:.5rem;margin:1rem 0}input{min-width:20rem;padding:.5rem}button{padding:.5rem .8rem}</style>
<h1>额度窗口激活器</h1>
<p class="muted">页面不嵌入凭证数据。管理密钥只保留在当前页面内存中。</p>
<div class="bar"><input id="key" type="password" autocomplete="off" placeholder="CPA Management key"><button id="load">加载状态</button><span id="notice"></span></div>
<table><thead><tr><th>Provider</th><th>Credential</th><th>Bucket</th><th>Used</th><th>Reset At</th><th>State</th><th>Next Check</th><th>Result</th></tr></thead><tbody id="rows"></tbody></table>
<script>
const rows=document.getElementById('rows'),notice=document.getElementById('notice'),key=document.getElementById('key');
document.getElementById('load').onclick=load;key.onkeydown=x=>{if(x.key==='Enter')load()};
async function load(){const value=key.value.trim();if(!value){notice.textContent='请输入 Management key';return}notice.textContent='加载中…';try{const auth=value.toLowerCase().startsWith('bearer ')?value:'Bearer '+value;const r=await fetch('/v0/management/plugins/quota-window-activator/status',{headers:{Authorization:auth},cache:'no-store'});if(!r.ok)throw new Error('HTTP '+r.status);const s=await r.json();rows.innerHTML=(s.windows||[]).map(w=>'<tr><td>'+e(w.provider)+'</td><td>'+e(w.auth_id)+'</td><td><code>'+e(w.bucket_id)+'</code></td><td>'+e(w.latest&&w.latest.used_percent)+'</td><td>'+e(w.latest&&w.latest.reset_at)+'</td><td>'+e(w.state)+'</td><td>'+e(w.next_check)+'</td><td>'+e(w.last_result)+'</td></tr>').join('');notice.textContent='已更新 '+new Date().toLocaleTimeString()}catch(x){notice.textContent='加载失败：'+x.message}}
function e(x){return String(x??'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]))}
</script></html>`
