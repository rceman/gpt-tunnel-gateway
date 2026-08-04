import json, os, subprocess, sys, tempfile, threading, unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

ROOT=Path(__file__).resolve().parents[1]; TOOL=ROOT/'scripts/check-github-ci.py'; SHA='a'*40

class Handler(BaseHTTPRequestHandler):
    payload={}; jobs_payload={"jobs":[]}; calls=0; headers={}; jobs_status=200; runs_status=200
    def do_GET(self):
        type(self).calls += 1; type(self).headers=dict(self.headers)
        is_jobs='/jobs' in self.path; status=type(self).jobs_status if is_jobs else type(self).runs_status
        source=type(self).jobs_payload if is_jobs else type(self).payload
        if isinstance(source,list) and source and isinstance(source[0],dict) and 'workflow_runs' in source[0]: body=json.dumps(source.pop(0)).encode()
        else: body=json.dumps(source).encode()
        self.send_response(status); self.send_header('Content-Type','application/json'); self.send_header('Content-Length',str(len(body))); self.end_headers(); self.wfile.write(body)
    def log_message(self,*args): pass

class CICapabilityTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.server=ThreadingHTTPServer(('127.0.0.1',0),Handler); threading.Thread(target=cls.server.serve_forever,daemon=True).start(); cls.api=f'http://127.0.0.1:{cls.server.server_port}'
    @classmethod
    def tearDownClass(cls): cls.server.shutdown()
    def setUp(self): Handler.calls=0; Handler.payload={'workflow_runs':[]}; Handler.jobs_payload={'jobs':[]}; Handler.jobs_status=200; Handler.runs_status=200; os.environ.pop('GITHUB_TOKEN',None)
    def run_tool(self, policy='auto', fmt='json', extra=(), sha=SHA, sha_from_git=None, cwd=None, env=None):
        e=os.environ.copy(); e.update(env or {})
        command=[sys.executable,str(TOOL),'--repository','owner/repo']
        if sha is not None:
            command.extend(['--sha',sha])
        if sha_from_git is not None:
            command.extend(['--sha-from-git',sha_from_git])
        command.extend(['--policy',policy,'--format',fmt,'--api-url',self.api,*extra])
        return subprocess.run(command,capture_output=True,text=True,env=e,cwd=cwd)
    def run_data(self, **kw): return json.loads(self.run_tool(**kw).stdout)
    def test_disabled_zero_requests(self): self.assertEqual(self.run_data(policy='disabled')['state'],'not_applicable'); self.assertEqual(Handler.calls,0)
    def test_success_exact_sha(self): Handler.payload={'workflow_runs':[{'id':1,'head_sha':SHA,'status':'completed','conclusion':'success','name':'Validate','event':'push','html_url':'u'}]}; d=self.run_data(); self.assertEqual(d['state'],'success'); self.assertEqual(d['checked_sha'],SHA)
    def test_sha_from_git_head_uses_exact_commit(self):
        with tempfile.TemporaryDirectory() as directory:
            repo=Path(directory)
            subprocess.run(['git','init'],cwd=repo,check=True,capture_output=True,text=True)
            subprocess.run(['git','config','user.email','ci@example.invalid'],cwd=repo,check=True)
            subprocess.run(['git','config','user.name','CI Test'],cwd=repo,check=True)
            (repo/'tracked.txt').write_text('tracked\n',encoding='utf-8')
            subprocess.run(['git','add','tracked.txt'],cwd=repo,check=True)
            subprocess.run(['git','commit','-m','fixture'],cwd=repo,check=True,capture_output=True,text=True)
            expected=subprocess.run(['git','rev-parse','HEAD'],cwd=repo,check=True,capture_output=True,text=True).stdout.strip()
            Handler.payload={'workflow_runs':[{'id':1,'head_sha':expected,'status':'completed','conclusion':'success','html_url':'u'}]}
            p=self.run_tool(sha=None,sha_from_git='HEAD',cwd=repo)
            data=json.loads(p.stdout)
            self.assertEqual(p.returncode,0)
            self.assertEqual(data['sha'],expected)
            self.assertEqual(data['checked_sha'],expected)
            self.assertGreater(Handler.calls,0)
    def test_sha_input_contract_and_invalid_git_ref(self):
        no_input=self.run_tool(sha=None)
        self.assertEqual(no_input.returncode,5)
        self.assertTrue(json.loads(no_input.stdout)['blocking'])
        both=self.run_tool(sha=SHA,sha_from_git='HEAD')
        self.assertEqual(both.returncode,5)
        with tempfile.TemporaryDirectory() as directory:
            empty=self.run_tool(sha=None,sha_from_git='HEAD',cwd=Path(directory))
            self.assertEqual(empty.returncode,5)
        invalid_ref=self.run_tool(sha=None,sha_from_git='HEAD~1')
        self.assertEqual(invalid_ref.returncode,5)
        self.assertEqual(Handler.calls,0)
    def test_wrong_sha_rejected(self): Handler.payload={'workflow_runs':[{'id':1,'head_sha':'b'*40,'status':'completed','conclusion':'success'}]}; self.assertEqual(self.run_data()['state'],'no_run')
    def test_pending_without_wait(self): Handler.payload={'workflow_runs':[{'id':1,'head_sha':SHA,'status':'queued','conclusion':None}]}; p=self.run_tool(); self.assertEqual(p.returncode,2); self.assertEqual(json.loads(p.stdout)['state'],'pending')
    def test_pending_wait_timeout(self): Handler.payload={'workflow_runs':[{'id':1,'head_sha':SHA,'status':'queued','conclusion':None}]}; p=self.run_tool(extra=('--wait','--timeout','1','--interval','1')); self.assertEqual(p.returncode,6)
    def test_wait_absorbs_no_run_then_pending_then_success(self):
        Handler.payload=[{'workflow_runs':[]},{'workflow_runs':[]},{'workflow_runs':[{'id':1,'head_sha':SHA,'status':'queued','conclusion':None}]},{'workflow_runs':[{'id':1,'head_sha':SHA,'status':'completed','conclusion':'success','html_url':'u'}]}]
        p=self.run_tool(policy='required',extra=('--wait','--timeout','5','--interval','1')); self.assertEqual(p.returncode,0); self.assertEqual(json.loads(p.stdout)['state'],'success')
    def test_wait_permanent_no_run_times_out(self):
        Handler.payload={'workflow_runs':[]}; p=self.run_tool(policy='required',extra=('--wait','--timeout','1','--interval','1')); self.assertEqual(p.returncode,6); self.assertEqual(json.loads(p.stdout)['state'],'timed_out')
    def test_failure_required(self): Handler.payload={'workflow_runs':[{'id':1,'head_sha':SHA,'status':'completed','conclusion':'failure'}]}; self.assertEqual(self.run_tool(policy='required').returncode,3)
    def test_neutral_active_policies_block(self):
        Handler.payload={'workflow_runs':[{'id':1,'head_sha':SHA,'status':'completed','conclusion':'neutral'}]}
        for policy in ('required','auto','optional'):
            p=self.run_tool(policy=policy); self.assertEqual(p.returncode,3); self.assertTrue(json.loads(p.stdout)['blocking'])
    def test_skipped_active_policies_block(self):
        Handler.payload={'workflow_runs':[{'id':1,'head_sha':SHA,'status':'completed','conclusion':'skipped'}]}
        for policy in ('required','auto','optional'):
            p=self.run_tool(policy=policy); self.assertEqual(p.returncode,3); self.assertTrue(json.loads(p.stdout)['blocking'])
    def test_failure_jobs_http_failure_preserves_failure(self):
        Handler.payload={'workflow_runs':[{'id':1,'head_sha':SHA,'status':'completed','conclusion':'failure'}]}; Handler.jobs_status=500
        p=self.run_tool(policy='auto'); self.assertEqual(p.returncode,3); self.assertEqual(json.loads(p.stdout)['state'],'failed')
    def test_failure_malformed_jobs_preserves_failure(self):
        Handler.payload={'workflow_runs':[{'id':1,'head_sha':SHA,'status':'completed','conclusion':'failure'}]}; Handler.jobs_payload=[]
        p=self.run_tool(policy='auto'); self.assertEqual(p.returncode,3); self.assertEqual(json.loads(p.stdout)['state'],'failed')
    def test_pending_jobs_failure_preserves_pending(self):
        Handler.payload={'workflow_runs':[{'id':1,'head_sha':SHA,'status':'queued','conclusion':None}]}; Handler.jobs_status=500
        p=self.run_tool(policy='auto'); self.assertEqual(p.returncode,2); self.assertEqual(json.loads(p.stdout)['state'],'pending')
    def test_success_jobs_failure_preserves_success(self):
        Handler.payload={'workflow_runs':[{'id':1,'head_sha':SHA,'status':'completed','conclusion':'success'}]}; Handler.jobs_status=500
        p=self.run_tool(policy='auto'); d=json.loads(p.stdout); self.assertEqual(p.returncode,0); self.assertEqual(d['state'],'success'); self.assertIsNone(d['job_id'])
    def test_failure_auto(self): Handler.payload={'workflow_runs':[{'id':1,'head_sha':SHA,'status':'completed','conclusion':'failure'}]}; self.assertEqual(self.run_tool(policy='auto').returncode,3)
    def test_failure_optional(self): Handler.payload={'workflow_runs':[{'id':1,'head_sha':SHA,'status':'completed','conclusion':'failure'}]}; self.assertEqual(self.run_tool(policy='optional').returncode,3)
    def test_failure_disabled_no_query(self): self.assertEqual(self.run_tool(policy='disabled').returncode,0); self.assertEqual(Handler.calls,0)
    def test_auto_no_run_nonblocking(self): self.assertEqual(self.run_tool(policy='auto').returncode,0)
    def test_optional_no_run_nonblocking(self): self.assertEqual(self.run_tool(policy='optional').returncode,0)
    def test_required_no_run_blocking(self): self.assertEqual(self.run_tool(policy='required').returncode,4)
    def test_text_output(self): p=self.run_tool(fmt='text'); self.assertIn('no_run:',p.stdout)
    def test_json_schema_fields(self): d=self.run_data(); self.assertEqual(set(d),{'schema_version','repository','sha','policy','state','blocking','source','run_id','job_id','run_url','job_url','workflow','event','status','conclusion','checked_sha','message'})
    def test_no_cache_headers(self): self.run_tool(); self.assertEqual(Handler.headers['Cache-Control'],'no-cache'); self.assertEqual(Handler.headers['Pragma'],'no-cache'); self.assertIn('application/vnd.github+json',Handler.headers['Accept'])
    def test_token_header_only_when_set(self): self.run_tool(); self.assertNotIn('Authorization',Handler.headers); self.run_tool(env={'GITHUB_TOKEN':'secret'}); self.assertEqual(Handler.headers['Authorization'],'Bearer secret')
    def test_explicit_policy_not_visibility(self): self.assertEqual(self.run_data(policy='auto')['policy'],'auto'); self.assertEqual(self.run_data(policy='disabled')['policy'],'disabled')
    def test_event_filter(self): Handler.payload={'workflow_runs':[{'id':1,'head_sha':SHA,'status':'completed','conclusion':'success','event':'pull_request'}]}; self.assertEqual(self.run_data()['state'],'no_run')
    def test_workflow_filter(self): Handler.payload={'workflow_runs':[{'id':1,'head_sha':SHA,'status':'completed','conclusion':'success','event':'push','name':'Other'}]}; self.assertEqual(self.run_data(extra=('--workflow','Validate'))['state'],'no_run')
    def test_malformed_json(self): Handler.payload=[]; p=self.run_tool(); self.assertEqual(p.returncode,5); self.assertTrue(json.loads(p.stdout)['blocking'])

if __name__=='__main__': unittest.main()
