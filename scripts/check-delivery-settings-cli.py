#!/usr/bin/env python3
"""Compiled CLI acceptance; synthetic loopback transport, no live authority proof.
Build first and pass the binary path. Uses isolated config and fake credentials.
"""
import http.server
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import threading

BINARY = str(Path(sys.argv[1] if len(sys.argv) > 1 else 'bin/hadron').resolve())
SETTINGS = dict(batchWindowSeconds=120, busyDelaySeconds=600,
                stuckWindowSeconds=900, standbyEnabled=True, standbyEffective=False)
requests = []
refusal = None
transport_fail = False
missing = False
MESSAGE = 'Exact server sentence; whole settings patch was refused.'


class Handler(http.server.BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def do_GET(self):
        self.send_response(200)
        self.end_headers()
        self.wfile.write(b'{"capabilities":[]}')

    def do_POST(self):
        q = json.loads(self.rfile.read(int(self.headers['Content-Length'])))
        requests.append(q)
        assert self.headers['Authorization'] == 'Bearer hdr_user_synthetic'
        assert not self.headers.get('X-Hadron-Session')
        op = q['operationName']
        if op == 'TeamAppIdentity':
            data = {'app': {'id': 'app1', 'urn': 'hrn:app:acme.com:ops', 'name': 'Ops'}}
        elif op in ('TeamDeliverySettings', 'TeamDeliverySettingsSet'):
            assert all(field in q['query'] for field in SETTINGS), q['query']
            key = 'appDeliverySettings' if op == 'TeamDeliverySettings' else 'setAppDeliverySettings'
            data = {key: None if missing else SETTINGS}
        else:
            raise AssertionError(op)
        if transport_fail and op == 'TeamDeliverySettingsSet':
            self.send_response(503)
            self.end_headers()
            return
        payload = {'data': data}
        if refusal and op != 'TeamAppIdentity':
            payload = {'errors': [{'message': MESSAGE, 'extensions': {'code': refusal}}]}
        self.send_response(200)
        self.end_headers()
        self.wfile.write(json.dumps(payload).encode())


srv = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Handler)
threading.Thread(target=srv.serve_forever, daemon=True).start()
count = 0
try:
    with tempfile.TemporaryDirectory(prefix='delivery-settings-cli-') as work:
        env = os.environ.copy()
        env.update(HADRON_TOKEN='hdr_user_synthetic', HADRON_SERVER='', XDG_CONFIG_HOME=work)

        def run(args, mode=True, code=0):
            global count
            start = len(requests)
            cmd = [BINARY, 'team', 'delivery-settings', *args,
                   '--app', 'acme.com:ops', '--server', f'http://127.0.0.1:{srv.server_port}']
            if mode:
                cmd.append('--json')
            result = subprocess.run(cmd, input='', text=True, capture_output=True,
                                    env=env, cwd=work, timeout=20)
            assert result.returncode == code, (args, result.returncode, result.stdout, result.stderr)
            if mode:
                json.loads(result.stdout)
            count += 1
            return result, requests[start:]

        for mode in (False, True):
            result, calls = run(['get'], mode)
            assert [q['operationName'] for q in calls].count('TeamDeliverySettings') == 1
            if mode:
                assert json.loads(result.stdout) == SETTINGS
                assert len(calls) == 1  # no render-only identity query
            else:
                assert 'from --app' in result.stdout and 'ticker cadence floor' in result.stdout
                rows = {line.rsplit(None, 1)[0].strip(): line.rsplit(None, 1)[-1] for line in result.stdout.splitlines() if 'standby (' in line}
                assert rows == {'standby (stored)': 'true', 'standby (effective)': 'false'}, rows
            for args, expected in [
                (['set', '--standby=false'], {'standbyEnabled': False}),
                (['set', '--busy-delay-seconds', '700'], {'busyDelaySeconds': 700}),
                (['set', '--batch-window-seconds', '60', '--stuck-window-seconds', '1000'],
                 {'batchWindowSeconds': 60, 'stuckWindowSeconds': 1000}),
                (['set'], {}),
            ]:
                result, calls = run(args, mode)
                mutations = [q for q in calls if q['operationName'] == 'TeamDeliverySettingsSet']
                assert len(mutations) == 1 and mutations[0]['variables']['input'] == expected
                if mode:
                    assert json.loads(result.stdout) == SETTINGS
                elif expected == {}:
                    assert 'Current setting values (manager-only set)' in result.stdout and 'Delivery settings updated.' not in result.stdout
                else:
                    assert 'Delivery settings updated.' in result.stdout
            for refusal, code in [
                ('FORBIDDEN', 8), ('APP_NOT_FOUND', 4), ('BAD_USER_INPUT', 2),
                ('DELIVERY_SETTINGS_OUT_OF_RANGE', 2), ('DELIVERY_SETTINGS_CONFLICT', 2),
                ('TICKER_SETTINGS_OUT_OF_RANGE', 2), ('TICKER_SETTINGS_CONFLICT', 5),
                ('DELIVERY_STANDBY_UNAVAILABLE', 5), ('SESSION_NOT_LIVE', 1),
            ]:
                result, calls = run(['set', '--standby=true'], mode, code)
                if mode:
                    assert json.loads(result.stdout)['error']['message'] == MESSAGE
                else:
                    assert result.stderr.strip() == 'hadron: ' + MESSAGE, result.stderr
                assert len(calls) == 1 and calls[0]['operationName'] == 'TeamDeliverySettingsSet'
                result, calls = run(['get'], mode, code)
                assert len(calls) == 1 and calls[0]['operationName'] == 'TeamDeliverySettings'
            refusal = None
            transport_fail = True
            result, calls = run(['set', '--standby=false'], mode, 7)
            assert len(calls) == 1 and calls[0]['operationName'] == 'TeamDeliverySettingsSet'
            transport_fail = False
            missing = True
            result, calls = run(['get'], mode, 1)
            assert len(calls) == 1 and 'no delivery settings' in (result.stdout + result.stderr)
            missing = False
        for value in ('', 'null', 'default', '1.5', '2147483648', '-2147483649'):
            result, calls = run(['set', '--busy-delay-seconds=' + value], True, 2)
            assert calls == []
        for args in ([], ['get'], ['set']):
            result, calls = run([*args, '--help'], False)
            assert calls == []
            for text in ('cadence floor', '120', '600', '900', 'current limits'):
                assert text in result.stdout
            normalized = ' '.join(result.stdout.split())
            assert 'changing it changes ticker cadence' not in normalized
            assert 'Existing tickers keep their cadence' in normalized
            assert "Raising it above a live ticker's cadence is refused" in normalized
        print(f'PASS {count} compiled CLI cases; synthetic transport only, no live authority/persistence proof')
finally:
    srv.shutdown()
