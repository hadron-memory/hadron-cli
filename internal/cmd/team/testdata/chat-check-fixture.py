#!/usr/bin/env python3
"""Author's compiled-CLI loopback fixture. No real server or production writes.
Usage: python3 chat-check-fixture.py /absolute/path/to/hadron
"""
import http.server, json, os, pathlib, subprocess, sys, tempfile, threading

binary = os.path.abspath(sys.argv[1])
for mode, expected in [('messages',0),('empty',9),('lost',0),('uncertain',10),('refused',5),('legacy',0),('stale',10)]:
    state = dict(marks=0,pages=0,ops=[],cursor_reads=0)
    class Handler(http.server.BaseHTTPRequestHandler):
        def log_message(self,*args): pass
        def do_POST(self):
            q=json.loads(self.rfile.read(int(self.headers['Content-Length'])))
            op=q['operationName'];v=q['variables'];state['ops'].append(op)
            assert self.headers.get('X-Hadron-Session')=='s1',dict(self.headers)
            data={}
            if op=='CheckChatSession': data={'session':{'id':'s1','appId':'app','workerId':'worker','endedAt':None,'isLive':True}}
            elif op=='CheckTeamChannel': data={'teamChatMessages':{'channelId':'chan','channelIncarnation':2}}
            elif op=='CheckChatCursor':
                state['cursor_reads']+=1
                data={'channelReadState':{'channelId':'chan','channelIncarnation':2,'cursorIncarnation':(None if mode=='legacy' else 1 if mode=='stale' else 2) if state['cursor_reads']==1 else None,'lastSeenSeq':9999 if mode=='stale' else 10}}
            elif op=='CheckChatHead': data={'channelMessages':{'channelId':'chan','channelIncarnation':2,'items':[{'seq':900}]}}
            elif op=='CheckChatPage':
                assert 'advanceReadState: false' in q['query'] and v['since']==(0 if mode in ('legacy','stale') else 10) and v['incarnation']==2
                state['pages']+=1
                seqs=[] if mode=='empty' else {901:[600,850],600:[80],80:([5] if mode in ('legacy','stale') else []),5:[]}[v['before']]
                data={'channelMessages':{'channelId':'chan','channelIncarnation':2,'items':[dict(nodeId=str(n),seq=n,body='@all fixture '+str(n),at='2026-10-21',authorUserId=None,authorWorkerId=None,authorName=None,sessionId=None,replyToSeq=None,mentions=['all']) for n in seqs]}}
            elif op=='CheckChatMark':
                assert v['through']==900 and v['incarnation']==2 and v['channelRef']=='chan'
                state['marks']+=1
                assert list(pathlib.Path(scratch,'cache').glob('hadron-chat-check-*/messages.json')) or mode=='empty'
                if mode=='uncertain' or mode=='lost' and state['marks']==1:
                    self.send_response(502);self.end_headers();return
                if mode=='refused':
                    self.send_response(200);self.end_headers();self.wfile.write(json.dumps({'errors':[{'message':'PRIVATE_SENTINEL','extensions':{'code':'CHANNEL_INCARNATION_CHANGED'}}]}).encode());return
                data={'markOwnTeamChatRead':{'workerId':'worker','channelId':'chan','channelIncarnation':2,'cursorIncarnation':1 if mode=='stale' else 2,'lastSeenSeq':9999 if mode=='stale' else 900}}
            else: raise AssertionError(op)
            self.send_response(200);self.send_header('Content-Type','application/json');self.end_headers();self.wfile.write(json.dumps({'data':data}).encode())
    with tempfile.TemporaryDirectory() as scratch:
        server=http.server.HTTPServer(('127.0.0.1',0),Handler)
        url='http://127.0.0.1:'+str(server.server_port)
        worker=threading.Thread(target=server.serve_forever,daemon=True);worker.start()
        env=dict(os.environ,HADRON_TOKEN='fixture',HADRON_TEAM_GIT_DIR=scratch,XDG_CONFIG_HOME=scratch+'/config',XDG_CACHE_HOME=scratch+'/cache')
        # Match the production binding filename.
        pathlib.Path(scratch,'hadron-team-session.json').write_text(json.dumps(dict(appId='app',workerId='worker',sessionId='s1',server=url)))
        result=subprocess.run([binary,'--server',url,'--json','team','chat','check'],env=env,text=True,capture_output=True,timeout=20)
        server.shutdown();worker.join()
        assert result.returncode==expected,(mode,result.returncode,result.stdout,result.stderr)
        receipt=json.loads(result.stdout)  # exactly ONE JSON document, through the real root renderer
        assert receipt['sampledHead']==900 and 'PRIVATE_SENTINEL' not in result.stdout+result.stderr
        if mode!='empty':
            artifact=json.loads(pathlib.Path(receipt['artifactPath']).read_text())
            assert [m['seq'] for m in artifact['messages']]==([5,80,600,850] if mode in ('legacy','stale') else [80,600,850])
        assert state['marks']==(2 if mode in ('lost','uncertain') else 1),state
        print(mode+': PASS exit='+str(expected)+' marks='+str(state['marks'])+' pages='+str(state['pages']))
