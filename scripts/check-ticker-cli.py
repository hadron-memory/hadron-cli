#!/usr/bin/env python3
"""Offline compiled-CLI acceptance. Build first; run with a binary path.

Uses only loopback fake GraphQL, isolated config and synthetic credentials.
Does not test real server authority/persistence or satisfy independent QA.
"""
import http.server,threading,tempfile,subprocess,json,os,sys
from pathlib import Path
T={'id':'cticker000000000000000000','urn':None,'name':'daily','slug':'daily','app':{'id':'app1'},'cadenceSeconds':600,'anchorAt':'2026-10-19T00:00:00Z','quietHours':None,'channels':[],'relevance':'ALL','reasons':['READ_TEAM_CHANNEL','EVERY_TICK'],'enabled':True,'owner':None,'nextTickAt':None,'lastTickAt':None,'createdAt':'2026-10-19T00:00:00Z','updatedAt':None,'tickCode':'tick.daily','registerTaskLoc':None}
S={'id':'s1','worker':{'id':'w1','name':'W','urn':None},'createdBy':None,'viaGrant':False,'createdAt':T['createdAt'],'endedAt':None,'endedReason':None}
binary = str(Path(sys.argv[1] if len(sys.argv) > 1 else 'bin/hadron').resolve())
requests=[];refusal=False;warn=False
class H(http.server.BaseHTTPRequestHandler):
 def log_message(self,*a):pass
 def do_GET(self):
  self.send_response(200);self.end_headers();self.wfile.write(b'{"capabilities":[]}')
 def do_POST(self):
  q=json.loads(self.rfile.read(int(self.headers['Content-Length'])));requests.append(q);op=q['operationName'];data={}
  assert self.headers['Authorization']=='Bearer hdr_user_synthetic'
  if op in ['CreateTicker','UpdateTicker']:data={op[0].lower()+op[1:]:T}
  elif op=='GetTicker':data={'ticker':T}
  elif op=='ListTickers':data={'tickers':[T]}
  elif op=='TickerSubscribers':data={'ticker':{'subscribers':{'items':[S],'hasNextPage':False,'endCursor':None}}}
  elif op=='DeleteTicker':data={'deleteTicker':True}
  elif op in ['SubscribeTicker','UnsubscribeTicker']:data={('subscribeWorker' if op=='SubscribeTicker' else 'unsubscribeWorker'):{'subscription':S,'announcement':'FAILED'}}
  elif op=='TeamAppIdentity':data={'app':{'id':'app1','urn':'hrn:app:acme.com:ops','name':'Ops'}}
  else:raise AssertionError(op)
  payload={'data':data}
  if warn and op=='CreateTicker':payload['extensions']={'warnings':[{'path':'createTicker','code':'ticker.code.unmapped','text':'No register entry maps tick.daily.'}]}
  if refusal and op=='UpdateTicker':payload={'errors':[{'message':'Whole edit refused: subscriber consent required','extensions':{'code':'TICKER_CADENCE_NEEDS_CONSENT'}}]}
  self.send_response(200);self.end_headers();self.wfile.write(json.dumps(payload).encode())
srv=http.server.ThreadingHTTPServer(('127.0.0.1',0),H);threading.Thread(target=srv.serve_forever,daemon=True).start()
with tempfile.TemporaryDirectory(prefix='joost923-compiled-') as d:
 env=os.environ.copy();env.update(HADRON_TOKEN='hdr_user_synthetic',HADRON_SERVER='',XDG_CONFIG_HOME=d)
 def run(args,jsonmode=True,code=0):
  before=len(requests);cmd=[binary,'ticker',*args,'--server',f'http://127.0.0.1:{srv.server_port}']
  if jsonmode:cmd+=['--json']
  r=subprocess.run(cmd,input='',text=True,capture_output=True,cwd=d,env=env);assert r.returncode==code,(args,r.returncode,r.stderr,r.stdout)
  if jsonmode:json.loads(r.stdout)
  return r,requests[before:]
 count=0
 for jm in [True,False]:
  for args in [ ['create','--app','acme.com:ops','--name','daily','--cadence-seconds','600','--reasons','every-tick,read-team-channel'],['update',T['id'],'--enabled=false','--clear-channels'],['get',T['id']],['list','--app','acme.com:ops'],['delete',T['id'],'--yes'],['subscribe',T['id'],'--worker','cworker000000000000000000'],['unsubscribe',T['id'],'--worker','cworker000000000000000000'] ]:
   r,qs=run(args,jm);count+=1
   if args[0] in ['subscribe','unsubscribe']:assert 'change stands' in r.stderr and len(qs)==1
   if args[0]=='update':assert qs[0]['variables']['input']=={'enabled':False,'clearChannels':True}
   if args[0]=='create':assert qs[0]['variables']['input']['reasons']==['READ_TEAM_CHANNEL','EVERY_TICK']
 for args in [['create','--name','n','--cadence-seconds','600','--reasons='],['update',T['id'],'--reasons','bogus'],['delete',T['id']]]:
  r,qs=run(args,True,2);assert not qs;count+=1
 # Round-trip the exact create-printed URNs, including Celine's e2e-alpha
 # fixture and a dotted root. No name-to-ref resolver or v1 parser involved.
 for root in ['e2e-alpha','e2e.com']:
  T['urn']=f'hrn:ticker:{root}:team:qa-t1'
  r,qs=run(['create','--app',f'{root}:team','--name','qa-t1','--cadence-seconds','600']);count+=1
  printed=json.loads(r.stdout)['urn'];assert printed==T['urn']
  for verb in ['get','update','delete','subscribe','unsubscribe']:
   args=[verb,printed]
   if verb=='update':args+=['--name','renamed']
   if verb=='delete':args+=['--yes']
   if verb in ['subscribe','unsubscribe']:args+=['--worker',f'hrn:worker:{root}:team:test-coordinator']
   r,qs=run(args);count+=1
   key='tickerRef' if verb in ['subscribe','unsubscribe'] else 'ref'
   assert qs[0]['variables'][key]==printed
   if key=='tickerRef':assert qs[0]['variables']['workerRef']==f'hrn:worker:{root}:team:test-coordinator'
 for args in [['get','hrn:node:e2e-alpha:team:qa-t1'],['get','hrn:ticker:e2e-alpha:team'],['subscribe',T['id'],'--worker','hrn:ticker:e2e-alpha:team:qa-t1'],['update',T['id']]]:
  r,qs=run(args,True,2);assert not qs;count+=1
  if args[0]=='update':assert 'nothing to update' in r.stdout
  else:assert 'pass a ' in r.stdout and 'unknown-type' not in r.stdout
 r,qs=run(['get',T['id']]);count+=1
 g=json.loads(r.stdout);assert g['tickCode']=='tick.daily' and g['registerTaskLoc'] is None
 warn=True
 cmd=['create','--app','acme.com:ops','--name','daily','--cadence-seconds','600']
 r,qs=run(cmd,False);count+=1;assert 'warning: [ticker.code.unmapped]' in r.stderr and 'warning' not in r.stdout
 r,qs=run(cmd,True);count+=1;assert json.loads(r.stdout)['warnings'][0]['code']=='ticker.code.unmapped' and r.stderr==''
 warn=False
 refusal=True;r,qs=run(['update',T['id'],'--reasons','every-tick'],True,8);assert len(qs)==1;count+=1
 for args in [[],['create'],['update'],['get'],['list'],['delete'],['subscribe'],['unsubscribe']]:
  r,qs=run(args+['--help'],False);assert not qs;count+=1
  assert 'Proposed' not in r.stdout and 'uncertain write' not in r.stdout
  if args and args[0] in ['create','update']:assert '(default true)' not in r.stdout
  if args and args[0]=='update':assert 'omitted keeps the current value' in r.stdout and 'creation time' not in r.stdout
 print(f'PASS {count} compiled CLI cases; isolated config/synthetic credential/fake GraphQL, both text and JSON; no real server/authority claim')
srv.shutdown()
