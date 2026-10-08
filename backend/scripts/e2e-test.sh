#!/usr/bin/env bash
# Disposable E2E fixtures only: no production services, volumes, or upstreams.
set -euo pipefail
cd "$(dirname "$0")/.."
: "${E2E_FIXTURE_IMAGE:?Set E2E_FIXTURE_IMAGE to an already-built local candidate image}"
export E2E_SOURCE_DIR="$PWD"
export E2E_GO_BIN="${E2E_GO_BIN:-$(command -v go || true)}"
: "${E2E_GO_BIN:?Go matching go.mod is required}"
"${E2E_PYTHON_BIN:-python3}" - <<'PY'
import json, os, pathlib, secrets, subprocess, tempfile, time, urllib.request, urllib.error, uuid
urllib.request.install_opener(urllib.request.build_opener(urllib.request.ProxyHandler({})))
source = pathlib.Path(os.environ['E2E_SOURCE_DIR'])
image = os.environ['E2E_FIXTURE_IMAGE']
run_id = uuid.uuid4().hex
prefix = 'sub2api-e2e-' + run_id[:12]
label = 'sub2api.fixture-run=' + run_id
network = prefix + '-net'
created = []
network_created = False

def run(args, timeout=60, **kwargs):
    return subprocess.check_output(args, timeout=timeout, stderr=subprocess.PIPE, **kwargs)

def start(name, args):
    created.append(name)
    run(['docker', 'run', '-d', '--name', name, '--label', label, *args])

request_js = """const [url,method,data,headers]=process.argv.slice(1);const req=require('http').request(url,{method:method,headers:JSON.parse(headers)},r=>{const chunks=[];r.on('data',b=>chunks.push(b));r.on('end',()=>console.log(JSON.stringify({status:r.statusCode,body:Buffer.concat(chunks).toString('base64')})));});req.on('error',()=>process.exit(1));req.end(data==='null'?undefined:Buffer.from(data,'base64'));"""

def raw_request(base, path, payload=None, token=None, method=None):
    import base64
    headers={'Content-Type':'application/json'}
    if token:headers['Authorization']='Bearer '+token
    method=method or ('GET' if payload is None else 'POST')
    encoded='null' if payload is None else base64.b64encode(json.dumps(payload).encode()).decode()
    response=json.loads(run(['docker','exec',client,'node','-e',request_js,base+path,method,encoded,json.dumps(headers)],timeout=20))
    return response['status'],base64.b64decode(response['body'])

def request(base, path, payload=None, token=None, method=None):
    status,body=raw_request(base,path,payload,token,method)
    assert status == 200, 'fixture API '+path+' returned HTTP '+str(status)
    result=json.loads(body)
    assert result.get('code') == 0 and isinstance(result.get('data'), dict), 'fixture API envelope'
    return result['data']

mock_js = r'''
const http=require('http');
const reply=(r,data)=>{r.setHeader('Content-Type','application/json');r.end(JSON.stringify(data));};
http.createServer((q,r)=>{let body='';q.on('data',b=>{body+=b;});q.on('end',()=>{
  const path=new URL(q.url,'http://synthetic-mock').pathname;
  let p={};try{if(body)p=JSON.parse(body);}catch{r.statusCode=400;return reply(r,{error:'invalid JSON'});}
  if(path.startsWith('/v1beta/models')&&q.method==='GET')return reply(r,{models:[{name:'models/gemini-2.5-flash',supportedGenerationMethods:['generateContent']} ]});
  if(path==='/v1/models'&&q.method==='GET')return reply(r,{object:'list',data:[{id:'claude-sonnet-4-5',object:'model'}]});
  if(path==='/v1/messages'&&q.method==='POST'){
    const m={id:'msg_synthetic',type:'message',role:'assistant',model:p.model,content:[{type:'text',text:'Hello synthetic fixture'}],stop_reason:'end_turn',stop_sequence:null,usage:{input_tokens:5,output_tokens:3}};
    if(!p.stream)return reply(r,m);
    r.setHeader('Content-Type','text/event-stream');const event=(name,data)=>r.write('event: '+name+'\ndata: '+JSON.stringify(data)+'\n\n');
    event('message_start',{type:'message_start',message:{...m,content:[],stop_reason:null,usage:{input_tokens:5,output_tokens:0}}});
    event('content_block_start',{type:'content_block_start',index:0,content_block:{type:'text',text:''}});
    event('content_block_delta',{type:'content_block_delta',index:0,delta:{type:'text_delta',text:'Hello synthetic fixture'}});
    event('content_block_stop',{type:'content_block_stop',index:0});
    event('message_delta',{type:'message_delta',delta:{stop_reason:'end_turn',stop_sequence:null},usage:{output_tokens:3}});
    event('message_stop',{type:'message_stop'});return r.end();
  }
  if(path.includes(':generateContent')||path.includes(':streamGenerateContent')){
    const m={candidates:[{content:{parts:[{text:'Hello synthetic fixture'}],role:'model'},finishReason:'STOP',index:0}],usageMetadata:{promptTokenCount:5,candidatesTokenCount:3,totalTokenCount:8}};
    if(path.includes(':streamGenerateContent')){r.setHeader('Content-Type','text/event-stream');return r.end('data: '+JSON.stringify(m)+'\n\n');}
    return reply(r,m);
  }
  r.statusCode=404;reply(r,{error:'unsupported synthetic endpoint'});
});}).listen(9000,'0.0.0.0');
'''

with tempfile.TemporaryDirectory(prefix='sub2api-e2e-') as tmp:
  try:
    metadata = json.loads(run(['docker', 'image', 'inspect', image]))[0]
    labels = metadata.get('Config', {}).get('Labels') or {}
    print(json.dumps({'fixture_image': image, 'image_id':metadata['Id'], 'revision':labels.get('org.opencontainers.image.revision'), 'scope':'synthetic-native-protocols; Antigravity-only cases remain explicit skips'}), flush=True)
    binary=pathlib.Path(tmp)/'e2e.test'
    client_arch=json.loads(run(['docker','image','inspect','node:24-alpine']))[0]['Architecture']
    compile_env=dict(os.environ,CGO_ENABLED='0',GOOS='linux',GOARCH=client_arch)
    subprocess.run([os.environ['E2E_GO_BIN'],'test','-tags=e2e','-c','-o',str(binary),'./internal/integration'],cwd=source,env=compile_env,check=True,timeout=300)
    network_created = True
    run(['docker','network','create','--internal','--label',label,network])
    db, redis, mock, app, client = [prefix+s for s in ('-db','-redis','-mock','-app','-client')]
    password = secrets.token_urlsafe(24)
    start(db, ['--network',network,'--network-alias','synthetic-db','--memory','512m','-e','POSTGRES_PASSWORD='+password,'-e','POSTGRES_DB=synthetic_e2e','postgres:18-alpine'])
    start(redis, ['--network',network,'--network-alias','synthetic-redis','--memory','128m','redis:8.4-alpine','redis-server','--save','','--appendonly','no'])
    for attempt in range(60):
        ready = subprocess.run(['docker','exec',db,'pg_isready','-U','postgres','-d','synthetic_e2e'],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,timeout=5)
        if ready.returncode == 0: break
        time.sleep(1)
    assert ready.returncode == 0, 'synthetic Postgres readiness'
    mock_path=pathlib.Path(tmp)/'mock.js';mock_path.write_text(mock_js)
    start(mock,['--network',network,'--network-alias','synthetic-mock','--read-only','--cap-drop','ALL','--memory','128m','-v',str(mock_path)+':/mock.js:ro','--entrypoint','node','node:24-alpine','/mock.js'])
    start(client,['--network',network,'--read-only','--cap-drop','ALL','--memory','256m','-v',str(binary)+':/e2e.test:ro','--entrypoint','node','node:24-alpine','-e','setInterval(()=>{},3600000)'])
    admin_email='synthetic-admin@example.invalid';admin_password=secrets.token_urlsafe(24)
    env={'AUTO_SETUP':'true','DATABASE_HOST':'synthetic-db','DATABASE_USER':'postgres','DATABASE_PASSWORD':password,'DATABASE_DBNAME':'synthetic_e2e','DATABASE_SSLMODE':'disable','REDIS_HOST':'synthetic-redis','SERVER_HOST':'0.0.0.0','SERVER_MODE':'release','ADMIN_EMAIL':admin_email,'ADMIN_PASSWORD':admin_password,'JWT_SECRET':secrets.token_hex(32),'TOTP_ENCRYPTION_KEY':secrets.token_hex(32),'HTTP_PROXY':'','HTTPS_PROXY':'','ALL_PROXY':'','NO_PROXY':'*'}
    args=['--network',network,'--network-alias','synthetic-app','--read-only','--cap-drop','ALL','--memory','1g','--user','1000:1000','--tmpfs','/app/data:rw,size=64m,uid=1000,gid=1000,mode=0700','--tmpfs','/tmp:rw,size=64m,mode=1777','--entrypoint','/app/sub2api']
    for key,value in env.items():args+=['-e',key+'='+value]
    start(app,[*args,image])
    base='http://synthetic-app:8080'
    for attempt in range(120):
        try:
            status,_=raw_request(base,'/health')
            if status==200:break
        except (OSError,subprocess.CalledProcessError):pass
        time.sleep(1)
    else:raise RuntimeError('synthetic app readiness failed')
    assert json.loads(run(['docker','network','inspect',network]))[0]['Internal'], 'fixture network isolation'
    sql="UPDATE users SET balance=100 WHERE role='admin'; INSERT INTO settings (key,value) VALUES ('default_balance','10') ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value;"
    run(['docker','exec','-i',db,'psql','-v','ON_ERROR_STOP=1','-U','postgres','-d','synthetic_e2e'],input=sql.encode())
    token=request(base,'/api/v1/auth/login',{'email':admin_email,'password':admin_password})['access_token']
    compliance=request(base,'/api/v1/admin/compliance',token=token)
    request(base,'/api/v1/admin/compliance/accept',{'phrase':compliance['ack_phrase_en'],'language':'en'},token)
    settings=request(base,'/api/v1/admin/settings',token=token)
    assert settings.get('email_verify_enabled') is not True, 'synthetic fixture has no email delivery service'
    assert settings.get('backend_mode_enabled') is not True, 'synthetic fixture must expose user services'
    settings['registration_enabled']=True
    request(base,'/api/v1/admin/settings',settings,token,method='PUT')
    fixture_keys={}
    for platform,key_env in [('anthropic','CLAUDE_API_KEY'),('gemini','GEMINI_API_KEY')]:
        group=request(base,'/api/v1/admin/groups',{'name':'synthetic-'+platform,'platform':platform,'rate_multiplier':1},token)
        request(base,'/api/v1/admin/accounts',{'name':'synthetic-'+platform,'platform':platform,'type':'apikey','credentials':{'api_key':'synthetic-upstream-only','base_url':'http://synthetic-mock:9000'},'group_ids':[group['id']],'concurrency':20,'priority':1},token)
        key=request(base,'/api/v1/keys',{'name':'synthetic-'+platform,'group_id':group['id']},token)
        fixture_keys[key_env]=key['key']
        if platform == 'anthropic':fixture_keys['E2E_USER_GROUP_ID']=str(group['id'])
    args=['docker','exec']
    fixture_keys.update({'BASE_URL':base,'ENDPOINT_PREFIX':'','E2E_MOCK':'true','HTTP_PROXY':'','HTTPS_PROXY':'','ALL_PROXY':'','NO_PROXY':'*'})
    for key,value in fixture_keys.items():args+=['-e',key+'='+value]
    args += [client,'/e2e.test','-test.v','-test.timeout=300s']
    log_path=pathlib.Path(tmp)/'test.jsonl'
    with log_path.open('wb') as log:
        result=subprocess.run([os.environ['E2E_GO_BIN'],'tool','test2json','-t','-p','github.com/Wei-Shaw/sub2api/internal/integration',*args],cwd=source,timeout=330,stdout=log)
    text=log_path.read_text();print(text,end='',flush=True)
    allowed_skips={'TestClaudeMessagesWithGeminiModel','TestGeminiEndpointWithClaudeModel'}
    events=[json.loads(line) for line in text.splitlines()]
    skipped={event.get('Test') for event in events if event.get('Action')=='skip'}
    assert skipped == allowed_skips, 'unexpected synthetic E2E skips: '+repr(skipped)
    raise SystemExit(result.returncode)
  finally:
    cleanup_errors=[]
    try:
        ids=run(['docker','ps','-aq','--filter','label='+label],timeout=15).decode().split()
    except (OSError,subprocess.SubprocessError):ids=[];cleanup_errors.append('container inventory')
    for container_id in ids:
        try:
            run(['docker','rm','-f',container_id],timeout=30)
        except (OSError,subprocess.SubprocessError):cleanup_errors.append('container removal')
    if network_created:
        try:
            ids=run(['docker','network','ls','-q','--filter','label='+label],timeout=15).decode().split()
        except (OSError,subprocess.SubprocessError):ids=[];cleanup_errors.append('network inventory')
        for network_id in ids:
            try:
                run(['docker','network','rm',network_id],timeout=30)
            except (OSError,subprocess.SubprocessError):cleanup_errors.append('network removal')
    if cleanup_errors:raise RuntimeError('fixture cleanup failed: '+','.join(cleanup_errors))
    print(json.dumps({'fixture_cleanup':'passed','run_id':run_id}),flush=True)
PY
