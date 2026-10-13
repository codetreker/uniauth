#!/usr/bin/env python3
"""Verify the release tag contract and GHCR failure handling."""

import base64
from datetime import datetime, timedelta, timezone
from email.message import Message
import importlib.util
import io
import json
from pathlib import Path
import unittest
from unittest.mock import patch
from urllib.error import HTTPError
from urllib.parse import parse_qs, urlsplit


SPEC = importlib.util.spec_from_file_location("release_image", Path(__file__).with_name("release-image.py"))
release_image = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(release_image)

REPOSITORY = "codetreker/uniauth"
SHA = "abcdef01" + "2" * 32
DAY = datetime(2026, 10, 13, 12, tzinfo=timezone.utc)


def response(payload, links=(), raw=None):
    result = io.BytesIO(json.dumps(payload).encode() if raw is None else raw)
    result.headers = Message()
    for link in links:
        result.headers.add_header("Link", link)
    return result


def http_error(status, payload):
    return HTTPError("https://ghcr.io/v2/codetreker/uniauth/tags/list", status, "registry error", Message(), response(payload))


class ReleaseImageTests(unittest.TestCase):
    def reference(self, tags, released_at=DAY):
        with patch.object(release_image, "urlopen", side_effect=[response({"token": "bearer"}), response({"tags": tags})]):
            return release_image.image_reference(REPOSITORY, SHA, "publisher", "credential", released_at)

    def test_first_daily_release_and_hash_length(self):
        for tags in ([], None, ["sha-abcdef0122222222", "261012-99-abcdef01"]):
            with self.subTest(tags=tags):
                self.assertEqual(self.reference(tags), "ghcr.io/codetreker/uniauth:261013-1-abcdef01")

    def test_daily_sequence_uses_numeric_maximum_and_exact_format(self):
        tags = ["261013-10-12345678", "261013-2-abcdef01", "261013-01-12345678",
                "261013-99-123456789", "261013-100-ABCDEFGH", "261013-0-12345678",
                "261013-999-12345678-extra", "prefix-261013-999-12345678"]
        self.assertEqual(self.reference(tags), "ghcr.io/codetreker/uniauth:261013-11-abcdef01")

    def test_date_resets_at_utc_midnight(self):
        tags = ["261013-8-12345678"]
        before = datetime(2026, 10, 13, 23, 59, 59, tzinfo=timezone.utc)
        after = before + timedelta(seconds=1)
        self.assertEqual(self.reference(tags, before), "ghcr.io/codetreker/uniauth:261013-9-abcdef01")
        self.assertEqual(self.reference(tags, after), "ghcr.io/codetreker/uniauth:261014-1-abcdef01")
        offset = datetime(2026, 10, 14, 1, tzinfo=timezone(timedelta(hours=2)))
        self.assertEqual(self.reference(tags, offset), "ghcr.io/codetreker/uniauth:261013-9-abcdef01")

    def test_pagination_follows_short_empty_and_null_pages(self):
        pages = [
            response({"token": "bearer"}),
            response({"tags": ["261013-2-abcdef01"]}, ['<?n=100&last=261013-2-abcdef01>; rel="next"']),
            response({"tags": []}, ['</v2/codetreker/uniauth/tags/list?n=100&last=empty>; rel=next']),
            response({"tags": None}, ['<https://ghcr.io/v2/codetreker/uniauth/tags/list?n=100&last=null>; rel="next"']),
            response({"tags": ["261013-10-abcdef01"]}),
        ]
        with patch.object(release_image, "urlopen", side_effect=pages) as request:
            result = release_image.image_reference(REPOSITORY, SHA, "publisher", "credential", DAY)
        self.assertEqual(result, "ghcr.io/codetreker/uniauth:261013-11-abcdef01")
        self.assertEqual(request.call_count, 5)
        self.assertEqual(request.call_args_list[2].args[0].full_url,
                         "https://ghcr.io/v2/codetreker/uniauth/tags/list?n=100&last=261013-2-abcdef01")

    def test_next_link_can_follow_other_links_and_headers(self):
        pages = [response({"token": "bearer"}),
                 response({"tags": []}, ['<?last=previous>; rel="prev", <?last=next>; rel="next"',
                                         '<?last=first>; rel="first"']),
                 response({"tags": ["261013-4-abcdef01"]})]
        with patch.object(release_image, "urlopen", side_effect=pages):
            self.assertEqual(release_image.image_reference(REPOSITORY, SHA, "publisher", "credential", DAY),
                             "ghcr.io/codetreker/uniauth:261013-5-abcdef01")

    def test_authenticated_token_and_tag_requests_are_bounded(self):
        with patch.object(release_image, "urlopen", side_effect=[response({"token": "bearer"}), response({"tags": []})]) as request:
            release_image.image_reference("CodeTreker/UniAuth", SHA, "publisher", "credential", DAY)
        token_request = request.call_args_list[0].args[0]
        expected = base64.b64encode(b"publisher:credential").decode()
        self.assertEqual(token_request.get_header("Authorization"), f"Basic {expected}")
        self.assertEqual(parse_qs(urlsplit(token_request.full_url).query),
                         {"service": ["ghcr.io"], "scope": ["repository:codetreker/uniauth:pull,push"]})
        self.assertEqual(request.call_args_list[1].args[0].get_header("Authorization"), "Bearer bearer")
        for call in request.call_args_list:
            self.assertEqual(call.kwargs["timeout"], 30)

    def test_first_repository_name_unknown_starts_at_one(self):
        error = http_error(404, {"errors": [{"code": "NAME_UNKNOWN", "message": "unknown repository"}]})
        with patch.object(release_image, "urlopen", side_effect=[response({"token": "bearer"}), error]):
            self.assertEqual(release_image.image_reference(REPOSITORY, SHA, "publisher", "credential", DAY),
                             "ghcr.io/codetreker/uniauth:261013-1-abcdef01")

    def test_token_http_errors_propagate(self):
        for status in (401, 403, 404, 500):
            with self.subTest(status=status):
                error = http_error(status, {"errors": [{"code": "NAME_UNKNOWN"}]})
                with patch.object(release_image, "urlopen", side_effect=error):
                    with self.assertRaises(HTTPError) as raised:
                        release_image.image_reference(REPOSITORY, SHA, "publisher", "credential", DAY)
                self.assertIs(raised.exception, error)

    def test_tag_http_errors_and_other_404_responses_propagate(self):
        cases = [(401, {"errors": [{"code": "UNAUTHORIZED"}]}),
                 (403, {"errors": [{"code": "DENIED"}]}),
                 (500, {"errors": [{"code": "NAME_UNKNOWN"}]}),
                 (404, {"errors": [{"code": "MANIFEST_UNKNOWN"}]}),
                 (404, {"errors": [{"code": "NAME_UNKNOWN"}, {"code": "DENIED"}]}),
                 (404, {"errors": []}), (404, []), (404, {})]
        for status, payload in cases:
            with self.subTest(status=status, payload=payload):
                error = http_error(status, payload)
                with patch.object(release_image, "urlopen", side_effect=[response({"token": "bearer"}), error]):
                    with self.assertRaises(HTTPError) as raised:
                        release_image.image_reference(REPOSITORY, SHA, "publisher", "credential", DAY)
                self.assertIs(raised.exception, error)

    def test_later_page_name_unknown_is_a_failure(self):
        error = http_error(404, {"errors": [{"code": "NAME_UNKNOWN"}]})
        pages = [response({"token": "bearer"}), response({"tags": []}, ['<?last=next>; rel="next"']), error]
        with patch.object(release_image, "urlopen", side_effect=pages):
            with self.assertRaises(HTTPError) as raised:
                release_image.image_reference(REPOSITORY, SHA, "publisher", "credential", DAY)
        self.assertIs(raised.exception, error)

    def test_malformed_error_body_preserves_http_error(self):
        error = HTTPError("https://ghcr.io/v2/codetreker/uniauth/tags/list", 404, "registry error", Message(),
                          response(None, raw=b"not JSON"))
        with patch.object(release_image, "urlopen", side_effect=[response({"token": "bearer"}), error]):
            with self.assertRaises(HTTPError) as raised:
                release_image.image_reference(REPOSITORY, SHA, "publisher", "credential", DAY)
        self.assertIs(raised.exception, error)

    def test_invalid_token_responses_fail(self):
        for payload in ([], {}, {"token": None}, {"token": ""}, {"token": 12}):
            with self.subTest(payload=payload), patch.object(release_image, "urlopen", return_value=response(payload)):
                with self.assertRaisesRegex(ValueError, "nonempty token"):
                    release_image.image_reference(REPOSITORY, SHA, "publisher", "credential", DAY)

    def test_invalid_tag_responses_fail(self):
        for payload in ([], {}, {"tags": "tag"}, {"tags": {}}, {"tags": [12]}, {"tags": [None]}):
            with self.subTest(payload=payload), patch.object(release_image, "urlopen", side_effect=[response({"token": "bearer"}), response(payload)]):
                with self.assertRaises(ValueError):
                    release_image.image_reference(REPOSITORY, SHA, "publisher", "credential", DAY)

    def test_invalid_json_responses_fail(self):
        for pages in ([response(None, raw=b"not JSON")],
                      [response({"token": "bearer"}), response(None, raw=b"not JSON")]):
            with self.subTest(pages=len(pages)), patch.object(release_image, "urlopen", side_effect=pages):
                with self.assertRaises(json.JSONDecodeError):
                    release_image.image_reference(REPOSITORY, SHA, "publisher", "credential", DAY)

    def test_invalid_pagination_fails_without_sending_credentials(self):
        links = ['<https://example.com/tags/list>; rel="next"',
                 '<?n=100>; rel="next"', '<?last=a>; rel="next", <?last=b>; rel="next"']
        for link in links:
            with self.subTest(link=link), patch.object(release_image, "urlopen", side_effect=[response({"token": "bearer"}), response({"tags": []}, [link])]) as request:
                with self.assertRaises(ValueError):
                    release_image.image_reference(REPOSITORY, SHA, "publisher", "credential", DAY)
                self.assertEqual(request.call_count, 2)

    def test_main_outputs_full_reference_and_captures_utc_date_once(self):
        environment = {"GITHUB_REPOSITORY": REPOSITORY, "GITHUB_SHA": SHA,
                       "REGISTRY_USERNAME": "publisher", "REGISTRY_PASSWORD": "credential"}
        output = io.StringIO()
        before = datetime(2026, 10, 13, 23, 59, 59, tzinfo=timezone.utc)
        pages = [response({"token": "bearer"}), response({"tags": ["261013-8-12345678"]})]
        with patch.dict(release_image.os.environ, environment, clear=True), \
                patch.object(release_image, "datetime") as clock, \
                patch.object(release_image, "urlopen", side_effect=pages), patch("sys.stdout", output):
            clock.now.side_effect = [before, before + timedelta(seconds=1)]
            release_image.main()
        clock.now.assert_called_once_with(timezone.utc)
        self.assertEqual(output.getvalue(), "reference=ghcr.io/codetreker/uniauth:261013-9-abcdef01\n")

    def test_invalid_commit_hash_fails_before_registry_requests(self):
        with patch.object(release_image, "urlopen") as request:
            with self.assertRaisesRegex(ValueError, "full lowercase commit hash"):
                release_image.image_reference(REPOSITORY, "abcdef01", "publisher", "credential", DAY)
        request.assert_not_called()


if __name__ == "__main__":
    unittest.main()
