import json
import subprocess
import tempfile
import unittest
from pathlib import Path

from ci_capability_test_support import CICapabilityTestCase, Handler, SHA


class CICapabilityContractTests(CICapabilityTestCase):
    def test_text_output(self): p=self.run_tool(fmt='text'); self.assertIn('no_run:',p.stdout)
    def test_json_schema_fields(self): d=self.run_data(); self.assertEqual(set(d),{'schema_version','repository','sha','policy','state','blocking','outcome','source','run_id','job_id','run_url','job_url','workflow','workflow_path','event','branch','status','conclusion','checked_sha','jobs','message'})
    def test_completed_run_requires_complete_job_set(self):
        Handler.payload={'workflow_runs':[{'id':1,'head_sha':SHA,'status':'completed','conclusion':'success','name':'Validate','html_url':'https://github.com/owner/repo/actions/runs/1'}]}; Handler.jobs_payload={'total_count':2,'jobs':[{'id':101,'name':'Validate','html_url':'https://github.com/owner/repo/actions/runs/1/jobs/101','status':'completed','conclusion':'success'}]}
        p=self.run_tool(policy='required'); data=json.loads(p.stdout); self.assertEqual(p.returncode,5); self.assertEqual(data['state'],'job_set_mismatch'); self.assertEqual(data['outcome'],'BLOCKED_CANONICAL_TOOLING_GAP')
    def test_no_cache_headers(self): self.run_tool(); self.assertEqual(Handler.headers['Cache-Control'],'no-cache'); self.assertEqual(Handler.headers['Pragma'],'no-cache'); self.assertIn('application/vnd.github+json',Handler.headers['Accept'])
    def test_token_header_only_when_set(self): self.run_tool(); self.assertNotIn('Authorization',Handler.headers); self.run_tool(env={'GITHUB_TOKEN':'secret'}); self.assertEqual(Handler.headers['Authorization'],'Bearer secret')
    def test_explicit_policy_not_visibility(self): self.assertEqual(self.run_data(policy='auto')['policy'],'auto'); self.assertEqual(self.run_data(policy='disabled')['policy'],'disabled')
    def test_event_filter(self): Handler.payload={'workflow_runs':[{'id':1,'head_sha':SHA,'status':'completed','conclusion':'success','event':'pull_request'}]}; self.assertEqual(self.run_data()['state'],'no_run')
    def test_workflow_filter(self): Handler.payload={'workflow_runs':[{'id':1,'head_sha':SHA,'status':'completed','conclusion':'success','event':'push','name':'Other'}]}; self.assertEqual(self.run_data(extra=('--workflow','Validate'))['state'],'no_run')
    def test_malformed_json(self): Handler.payload=[]; p=self.run_tool(); self.assertEqual(p.returncode,5); self.assertTrue(json.loads(p.stdout)['blocking'])

if __name__ == "__main__":
    unittest.main()
