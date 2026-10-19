#!/usr/bin/env python3
"""Offline compiled-CLI acceptance. Build first; run with a binary path.

Uses only loopback fake GraphQL, isolated config and synthetic credentials.
Does not test real server authority/persistence or satisfy independent QA.
"""
import http.server,threading,tempfile,subprocess,json,os,sys
from pathlib import Path
T={'id':'cticker000000000000000000','urn':None,'name':'daily','slug':'daily','app':{'id':'app1'},'cadenceSeconds':600,'anchorAt':'2026-10-19T00:00:00Z','quietHours':None,'channels':[],'relevance':'ALL','reasons':['READ_TEAM_CHANNEL','EVERY_TICK'],'enabled':True,'owner':None,'nextTickAt':None,'lastTickAt':None,'createdAt':'2026-10-19T00:00:00Z','updatedAt':None}
S={'id':'s1','worker':{'id':'w1','name':'W','urn':None},'createdBy':None,'viaGrant':False,'createdAt':T['createdAt'],'endedAt':None,'endedReason':None}
binary = str(Path(sys.argv[1] if len(sys.argv) > 1 else 'bin/hadron').resolve())
requests=[];refusal=False
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
 refusal=True;r,qs=run(['update',T['id'],'--reasons','every-tick'],True,8);assert len(qs)==1;count+=1
 for args in [[],['create'],['update'],['get'],['list'],['delete'],['subscribe'],['unsubscribe']]:
  r,qs=run(args+['--help'],False);assert not qs;count+=1
 print(f'PASS {count} compiled CLI cases; isolated config/synthetic credential/fake GraphQL, both text and JSON; no real server/authority claim')
srv.shutdown()
