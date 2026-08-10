import json
import subprocess
import tempfile
import unittest
from pathlib import Path

from ci_capability_test_support import CICapabilityTestCase, Handler, SHA


class CICapabilityStateTests(CICapabilityTestCase):
    def test_pending_without_wait(self): Handler.payload={'workflow_runs':[{'id':1,'head_sha':SHA,'status':'queued','conclusion':None,'name':'Validate','html_url':'https://github.com/owner/repo/actions/runs/1'}]}; p=self.run_tool(); self.assertEqual(p.returncode,2); self.assertEqual(json.loads(p.stdout)['state'],'pending')
    def test_pending_wait_timeout(self): Handler.payload={'workflow_runs':[{'id':1,'head_sha':SHA,'status':'queued','conclusion':None,'name':'Validate','html_url':'https://github.com/owner/repo/actions/runs/1'}]}; p=self.run_tool(policy='required',extra=('--wait','--timeout','1','--interval','1')); d=json.loads(p.stdout); self.assertEqual(p.returncode,6); self.assertEqual(d['state'],'timed_out'); self.assertEqual(d['outcome'],'BLOCKED_CANONICAL_TOOLING_GAP'); self.assertTrue(d['blocking'])
    def test_wait_absorbs_no_run_then_pending_then_success(self):
        Handler.payload=[{'workflow_runs':[]},{'workflow_runs':[]},{'workflow_runs':[{'id':1,'head_sha':SHA,'status':'queued','conclusion':None,'name':'Validate','path':'.github/workflows/ci.yml','event':'push','head_branch':'main','html_url':'https://github.com/owner/repo/actions/runs/1'}]},{'workflow_runs':[{'id':1,'head_sha':SHA,'status':'completed','conclusion':'success','name':'Validate','path':'.github/workflows/ci.yml','event':'push','head_branch':'main','html_url':'https://github.com/owner/repo/actions/runs/1'}]}]
        p=self.run_tool(policy='required',extra=('--wait','--timeout','5','--interval','1')); self.assertEqual(p.returncode,0); self.assertEqual(json.loads(p.stdout)['state'],'success')
    def test_wait_permanent_no_run_times_out(self):
        Handler.payload={'workflow_runs':[]}; p=self.run_tool(policy='required',extra=('--wait','--timeout','1','--interval','1')); self.assertEqual(p.returncode,6); self.assertEqual(json.loads(p.stdout)['state'],'timed_out')
    def test_failure_required(self): Handler.payload={'workflow_runs':[{'id':1,'head_sha':SHA,'status':'completed','conclusion':'failure','name':'Validate','html_url':'https://github.com/owner/repo/actions/runs/1'}]}; self.assertEqual(self.run_tool(policy='required').returncode,3)
    def test_neutral_active_policies_block(self):
        Handler.payload={'workflow_runs':[{'id':1,'head_sha':SHA,'status':'completed','conclusion':'neutral','name':'Validate','html_url':'https://github.com/owner/repo/actions/runs/1'}]}
        for policy in ('required','auto','optional'):
            p=self.run_tool(policy=policy); self.assertEqual(p.returncode,3); self.assertTrue(json.loads(p.stdout)['blocking'])
    def test_skipped_active_policies_block(self):
        Handler.payload={'workflow_runs':[{'id':1,'head_sha':SHA,'status':'completed','conclusion':'skipped','name':'Validate','html_url':'https://github.com/owner/repo/actions/runs/1'}]}
        for policy in ('required','auto','optional'):
            p=self.run_tool(policy=policy); self.assertEqual(p.returncode,3); self.assertTrue(json.loads(p.stdout)['blocking'])
    def test_failure_jobs_http_failure_is_job_mismatch(self):
        Handler.payload={'workflow_runs':[{'id':1,'head_sha':SHA,'status':'completed','conclusion':'failure','name':'Validate','html_url':'https://github.com/owner/repo/actions/runs/1'}]}; Handler.jobs_status=500
        p=self.run_tool(policy='auto'); self.assertEqual(p.returncode,5); self.assertEqual(json.loads(p.stdout)['state'],'job_set_mismatch')
    def test_failure_malformed_jobs_is_job_mismatch(self):
        Handler.payload={'workflow_runs':[{'id':1,'head_sha':SHA,'status':'completed','conclusion':'failure','name':'Validate','html_url':'https://github.com/owner/repo/actions/runs/1'}]}; Handler.jobs_payload=[]
        p=self.run_tool(policy='auto'); self.assertEqual(p.returncode,5); self.assertEqual(json.loads(p.stdout)['state'],'job_set_mismatch')
    def test_pending_jobs_failure_preserves_pending(self):
        Handler.payload={'workflow_runs':[{'id':1,'head_sha':SHA,'status':'queued','conclusion':None,'name':'Validate','html_url':'https://github.com/owner/repo/actions/runs/1'}]}; Handler.jobs_status=500
        p=self.run_tool(policy='auto'); self.assertEqual(p.returncode,2); self.assertEqual(json.loads(p.stdout)['state'],'pending')
    def test_success_jobs_failure_is_job_mismatch(self):
        Handler.payload={'workflow_runs':[{'id':1,'head_sha':SHA,'status':'completed','conclusion':'success','name':'Validate','html_url':'https://github.com/owner/repo/actions/runs/1'}]}; Handler.jobs_status=500
        p=self.run_tool(policy='auto'); d=json.loads(p.stdout); self.assertEqual(p.returncode,5); self.assertEqual(d['state'],'job_set_mismatch'); self.assertTrue(d['blocking'])
    def test_failure_auto(self): Handler.payload={'workflow_runs':[{'id':1,'head_sha':SHA,'status':'completed','conclusion':'failure','name':'Validate','html_url':'https://github.com/owner/repo/actions/runs/1'}]}; self.assertEqual(self.run_tool(policy='auto').returncode,3)
    def test_failure_optional(self): Handler.payload={'workflow_runs':[{'id':1,'head_sha':SHA,'status':'completed','conclusion':'failure','name':'Validate','html_url':'https://github.com/owner/repo/actions/runs/1'}]}; self.assertEqual(self.run_tool(policy='optional').returncode,3)
    def test_failure_disabled_no_query(self): self.assertEqual(self.run_tool(policy='disabled').returncode,0); self.assertEqual(Handler.calls,0)
    def test_auto_no_run_nonblocking(self): self.assertEqual(self.run_tool(policy='auto').returncode,0)
    def test_optional_no_run_nonblocking(self): self.assertEqual(self.run_tool(policy='optional').returncode,0)
    def test_required_no_run_blocking(self): p=self.run_tool(policy='required'); d=json.loads(p.stdout); self.assertEqual(p.returncode,4); self.assertEqual(d['outcome'],'BLOCKED_CANONICAL_TOOLING_GAP'); self.assertTrue(d['blocking'])
    def test_required_transport_failures_are_fail_closed(self):
        unavailable=self.run_tool(policy='required', api_url='http://127.0.0.1:9'); unavailable_data=json.loads(unavailable.stdout)
        self.assertEqual(unavailable.returncode,4); self.assertEqual(unavailable_data['state'],'unavailable'); self.assertEqual(unavailable_data['outcome'],'BLOCKED_CANONICAL_TOOLING_GAP'); self.assertTrue(unavailable_data['blocking'])
        for status, expected_state, expected_exit in ((401, 'authentication_failure', 4), (403, 'rate_limited', 4), (404, 'not_found', 4), (500, 'api_failure', 5)):
            Handler.runs_status=status; Handler.rate_limited=status == 403
            p=self.run_tool(policy='required'); d=json.loads(p.stdout)
            self.assertEqual(p.returncode,expected_exit); self.assertEqual(d['state'],expected_state); self.assertEqual(d['outcome'],'BLOCKED_CANONICAL_TOOLING_GAP'); self.assertTrue(d['blocking'])

if __name__ == "__main__":
    unittest.main()
