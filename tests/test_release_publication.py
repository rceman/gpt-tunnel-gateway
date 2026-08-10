import unittest
import json

from release_publication_test_support import PublicationHandler, PublicationTestCase, publication


class ReleasePublicationProofTests(PublicationTestCase):
    def test_release_publication_success_proves_tag_ci_jobs_and_tag_only(self):
        directory, repo, commit = self.make_release_repo()
        self.addCleanup(directory.cleanup)
        PublicationHandler.run = {
            "id": 41,
            "head_sha": commit,
            "status": "completed",
            "conclusion": "success",
            "name": "Validate",
            "path": ".github/workflows/ci.yml",
            "event": "push",
            "head_branch": "main",
            "html_url": "https://github.com/owner/repo/actions/runs/41",
        }
        PublicationHandler.jobs = {"total_count": 1, "jobs": [{
            "id": 501,
            "name": "unit",
            "status": "completed",
            "conclusion": "success",
            "html_url": "https://github.com/owner/repo/actions/runs/41/jobs/501",
        }]}
        result = publication.verify(self.publication_args(repo, commit))
        self.assertEqual(result["state"], "success")
        self.assertEqual(result["peeled_commit"], commit)
        self.assertEqual(result["jobs"][0]["id"], 501)
        self.assertEqual(result["release"]["state"], "not_found_expected")
    def test_release_topology_is_loaded_from_exact_release_commit(self):
        directory, repo, commit = self.make_release_repo()
        self.addCleanup(directory.cleanup)
        (repo / "release-config.json").write_text(json.dumps({"publication": {"topology": "github_release", "github_release": True, "assets": ["mutable.zip"]}}), encoding="utf-8")
        PublicationHandler.run = {"id": 41, "head_sha": commit, "status": "completed", "conclusion": "success", "name": "Validate", "html_url": "https://github.com/owner/repo/actions/runs/41"}
        PublicationHandler.jobs = {"total_count": 1, "jobs": [{"id": 501, "name": "unit", "status": "completed", "conclusion": "success", "html_url": "https://github.com/owner/repo/actions/runs/41/jobs/501"}]}
        result = publication.verify(self.publication_args(repo, commit))
        self.assertFalse(result["release"]["declared"])
        self.assertEqual(result["release"]["state"], "not_found_expected")
    def test_release_publication_rejects_incomplete_job_proof(self):
        directory, repo, commit = self.make_release_repo()
        self.addCleanup(directory.cleanup)
        PublicationHandler.run = {
            "id": 41,
            "head_sha": commit,
            "status": "completed",
            "conclusion": "success",
            "name": "Validate",
            "html_url": "https://github.com/owner/repo/actions/runs/41",
        }
        PublicationHandler.jobs = {"total_count": 2, "jobs": [{"id": 501, "name": "unit", "status": "completed", "conclusion": "success", "html_url": "https://github.com/owner/repo/actions/runs/41/jobs/501"}]}
        with self.assertRaises(publication.PublicationError) as raised:
            publication.verify(self.publication_args(repo, commit))
        self.assertEqual(raised.exception.state, "ci_job_set_mismatch")
    def test_release_publication_emits_distinct_not_found_api_and_asset_states(self):
        directory, repo, commit = self.make_release_repo(github_release=True)
        self.addCleanup(directory.cleanup)
        PublicationHandler.run = {"id": 41, "head_sha": commit, "status": "completed", "conclusion": "success", "name": "Validate", "html_url": "https://github.com/owner/repo/actions/runs/41"}
        PublicationHandler.jobs = {"total_count": 1, "jobs": [{"id": 501, "name": "unit", "status": "completed", "conclusion": "success", "html_url": "https://github.com/owner/repo/actions/runs/41/jobs/501"}]}
        with self.assertRaises(publication.PublicationError) as raised:
            publication.verify(self.publication_args(repo, commit))
        self.assertEqual(raised.exception.state, "release_not_found")
        PublicationHandler.runs_status = 404
        with self.assertRaises(publication.PublicationError) as raised:
            publication.verify(self.publication_args(repo, commit))
        self.assertEqual(raised.exception.state, "ci_run_not_found")
        PublicationHandler.runs_status = 200; PublicationHandler.release_status = 200; PublicationHandler.release_payload = {"tag_name": "v9.9.9", "assets": [{"name": "unexpected.zip"}]}
        with self.assertRaises(publication.PublicationError) as raised:
            publication.verify(self.publication_args(repo, commit))
        self.assertEqual(raised.exception.state, "asset_mismatch")
        PublicationHandler.release_status = 404; PublicationHandler.release_payload = {"message": "not found"}; PublicationHandler.jobs_status = 500
        with self.assertRaises(publication.PublicationError) as raised:
            publication.verify(self.publication_args(repo, commit))
        self.assertEqual(raised.exception.state, "ci_api_failure")

if __name__ == "__main__":
    unittest.main()
