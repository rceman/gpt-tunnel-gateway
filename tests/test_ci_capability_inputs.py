import json
import subprocess
import tempfile
import unittest
from pathlib import Path

from ci_capability_test_support import CICapabilityTestCase, Handler, SHA


class CICapabilityInputTests(CICapabilityTestCase):
    def test_disabled_zero_requests(self): self.assertEqual(self.run_data(policy='disabled')['state'],'not_applicable'); self.assertEqual(Handler.calls,0)
    def test_success_exact_sha(self): Handler.payload={'workflow_runs':[{'id':1,'head_sha':SHA,'status':'completed','conclusion':'success','name':'Validate','path':'.github/workflows/ci.yml','event':'push','head_branch':'main','html_url':'https://github.com/owner/repo/actions/runs/1'}]}; d=self.run_data(); self.assertEqual(d['state'],'success'); self.assertEqual(d['checked_sha'],SHA); self.assertEqual(d['jobs'][0]['id'],101)
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
            Handler.payload={'workflow_runs':[{'id':1,'head_sha':expected,'status':'completed','conclusion':'success','name':'Validate','path':'.github/workflows/ci.yml','event':'push','head_branch':'main','html_url':'https://github.com/owner/repo/actions/runs/1'}]}
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

if __name__ == "__main__":
    unittest.main()
