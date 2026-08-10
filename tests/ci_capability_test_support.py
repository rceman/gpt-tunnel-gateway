import json, os, subprocess, sys, tempfile, threading, unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

ROOT=Path(__file__).resolve().parents[1]; TOOL=ROOT/'scripts/check-github-ci.py'; SHA='a'*40

class Handler(BaseHTTPRequestHandler):
    payload={}; jobs_payload={"total_count":1,"jobs":[{"id":101,"name":"Validate","html_url":"https://github.com/owner/repo/actions/runs/1/jobs/101","status":"completed","conclusion":"success"}]}; calls=0; headers={}; jobs_status=200; runs_status=200; rate_limited=False
    def do_GET(self):
        type(self).calls += 1; type(self).headers=dict(self.headers)
        is_jobs='/jobs' in self.path; status=type(self).jobs_status if is_jobs else type(self).runs_status
        source=type(self).jobs_payload if is_jobs else type(self).payload
        if isinstance(source,list) and source and isinstance(source[0],dict) and 'workflow_runs' in source[0]: body=json.dumps(source.pop(0)).encode()
        else: body=json.dumps(source).encode()
        self.send_response(status); self.send_header('Content-Type','application/json'); self.send_header('Content-Length',str(len(body)))
        if type(self).rate_limited: self.send_header('X-RateLimit-Remaining','0')
        self.end_headers(); self.wfile.write(body)
    def log_message(self,*args): pass

class CICapabilityTestCase(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.server=ThreadingHTTPServer(('127.0.0.1',0),Handler); cls.server_thread=threading.Thread(target=cls.server.serve_forever,daemon=True); cls.server_thread.start(); cls.api=f'http://127.0.0.1:{cls.server.server_port}'
    @classmethod
    def tearDownClass(cls): cls.server.shutdown(); cls.server_thread.join(timeout=5); cls.server.server_close()
    def setUp(self): Handler.calls=0; Handler.payload={'workflow_runs':[]}; Handler.jobs_payload={'total_count':1,'jobs':[{'id':101,'name':'Validate','html_url':'https://github.com/owner/repo/actions/runs/1/jobs/101','status':'completed','conclusion':'success'}]}; Handler.jobs_status=200; Handler.runs_status=200; Handler.rate_limited=False; os.environ.pop('GITHUB_TOKEN',None)
    def run_tool(self, policy='auto', fmt='json', extra=(), sha=SHA, sha_from_git=None, cwd=None, env=None, api_url=None):
        e=os.environ.copy(); e.update(env or {})
        command=[sys.executable,str(TOOL),'--repository','owner/repo']
        if sha is not None:
            command.extend(['--sha',sha])
        if sha_from_git is not None:
            command.extend(['--sha-from-git',sha_from_git])
        command.extend(['--policy',policy,'--format',fmt,'--api-url',api_url or self.api,*extra])
        return subprocess.run(command,capture_output=True,text=True,env=e,cwd=cwd)
    def run_data(self, **kw): return json.loads(self.run_tool(**kw).stdout)
