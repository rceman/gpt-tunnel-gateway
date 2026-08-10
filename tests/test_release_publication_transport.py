import unittest
from unittest import mock

from release_publication_test_support import PublicationHandler, PublicationTestCase, publication


class ReleasePublicationTransportTests(PublicationTestCase):
    def test_release_transport_states_are_preserved_by_endpoint(self):
        directory, repo, commit = self.make_release_repo()
        self.addCleanup(directory.cleanup)
        PublicationHandler.run = {"id": 41, "head_sha": commit, "status": "completed", "conclusion": "success", "name": "Validate", "html_url": "https://github.com/owner/repo/actions/runs/41"}
        PublicationHandler.jobs = {"total_count": 1, "jobs": [{"id": 501, "name": "unit", "status": "completed", "conclusion": "success", "html_url": "https://github.com/owner/repo/actions/runs/41/jobs/501"}]}
        for status, expected in ((503, "ci_api_failure"), (401, "authentication_failure"), (403, "rate_limited")):
            PublicationHandler.runs_status = status; PublicationHandler.rate_limited = status == 403
            with self.assertRaises(publication.PublicationError) as raised:
                publication.verify(self.publication_args(repo, commit))
            self.assertEqual(raised.exception.state, expected)
        PublicationHandler.runs_status = 200; PublicationHandler.rate_limited = False
        for status, expected in ((503, "ci_api_failure"), (401, "authentication_failure"), (403, "rate_limited")):
            PublicationHandler.jobs_status = status; PublicationHandler.rate_limited = status == 403
            with self.assertRaises(publication.PublicationError) as raised:
                publication.verify(self.publication_args(repo, commit))
            self.assertEqual(raised.exception.state, expected)
        PublicationHandler.jobs_status = 200; PublicationHandler.rate_limited = False
        for status, expected in ((503, "api_failure"), (401, "authentication_failure"), (403, "rate_limited")):
            PublicationHandler.release_status = status; PublicationHandler.rate_limited = status == 403
            with self.assertRaises(publication.PublicationError) as raised:
                publication.verify(self.publication_args(repo, commit))
            self.assertEqual(raised.exception.state, expected)
    def test_release_unavailable_transport_is_preserved_for_runs_jobs_and_release(self):
        directory, repo, commit = self.make_release_repo()
        self.addCleanup(directory.cleanup)
        PublicationHandler.run = {"id": 41, "head_sha": commit, "status": "completed", "conclusion": "success", "name": "Validate", "html_url": "https://github.com/owner/repo/actions/runs/41"}
        PublicationHandler.jobs = {"total_count": 1, "jobs": [{"id": 501, "name": "unit", "status": "completed", "conclusion": "success", "html_url": "https://github.com/owner/repo/actions/runs/41/jobs/501"}]}
        run_payload = {"workflow_runs": [PublicationHandler.run]}
        jobs_payload = PublicationHandler.jobs
        def fail_runs(url, token):
            raise publication.GitHubAPIError("unavailable", None, url, "test unavailable")
        with mock.patch.object(publication, "fetch_json", side_effect=fail_runs):
            with self.assertRaises(publication.PublicationError) as raised:
                publication.verify(self.publication_args(repo, commit))
        self.assertEqual(raised.exception.state, "unavailable")

        def fail_jobs(url, token):
            if "/jobs" in url:
                raise publication.GitHubAPIError("unavailable", None, url, "test unavailable")
            return run_payload
        with mock.patch.object(publication, "fetch_json", side_effect=fail_jobs):
            with self.assertRaises(publication.PublicationError) as raised:
                publication.verify(self.publication_args(repo, commit))
        self.assertEqual(raised.exception.state, "unavailable")

        def fail_release(url, token):
            if "/releases/" in url:
                raise publication.GitHubAPIError("unavailable", None, url, "test unavailable")
            if "/jobs" in url:
                return jobs_payload
            return run_payload
        with mock.patch.object(publication, "fetch_json", side_effect=fail_release):
            with self.assertRaises(publication.PublicationError) as raised:
                publication.verify(self.publication_args(repo, commit))
        self.assertEqual(raised.exception.state, "unavailable")
    def test_release_publication_preserves_typed_auth_failure(self):
        directory, repo, commit = self.make_release_repo()
        self.addCleanup(directory.cleanup)
        with self.assertRaises(publication.PublicationError) as raised:
            publication.verify(self.publication_args(repo, commit, "http://127.0.0.1:9"))
        self.assertEqual(raised.exception.state, "unavailable")

if __name__ == "__main__":
    unittest.main()
