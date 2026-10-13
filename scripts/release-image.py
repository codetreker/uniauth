#!/usr/bin/env python3
"""Allocate the next UTC daily image tag from the published GHCR tags."""

import base64
from datetime import datetime, timezone
import json
import os
import re
from urllib.error import HTTPError
from urllib.parse import urlencode, urljoin, urlsplit
from urllib.request import Request, urlopen


def registry_tags(repository, username, password):
    credentials = base64.b64encode(f"{username}:{password}".encode()).decode()
    token_url = "https://ghcr.io/token?" + urlencode({
        "service": "ghcr.io",
        "scope": f"repository:{repository}:pull,push",
    })
    request = Request(token_url, headers={"Authorization": f"Basic {credentials}"})
    with urlopen(request, timeout=30) as response:
        payload = json.load(response)
    if not isinstance(payload, dict) or not isinstance(payload.get("token"), str) or not payload["token"]:
        raise ValueError("GHCR token response must contain a nonempty token")

    tags_path = f"/v2/{repository}/tags/list"
    url = f"https://ghcr.io{tags_path}?n=100"
    visited = set()
    while url:
        if url in visited:
            raise ValueError("GHCR tag pagination repeated a page")
        visited.add(url)
        request = Request(url, headers={"Authorization": f"Bearer {payload['token']}"})
        try:
            with urlopen(request, timeout=30) as response:
                page = json.load(response)
                links = response.headers.get_all("Link", [])
        except HTTPError as error:
            if error.code == 404 and len(visited) == 1:
                try:
                    failure = json.load(error)
                except (ValueError, UnicodeDecodeError):
                    raise error
                errors = failure.get("errors") if isinstance(failure, dict) else None
                if (isinstance(errors, list) and errors
                        and all(isinstance(item, dict) and item.get("code") == "NAME_UNKNOWN" for item in errors)):
                    return
            raise
        if not isinstance(page, dict) or "tags" not in page:
            raise ValueError("GHCR tag response must contain tags")
        tags = page["tags"]
        if tags is not None:
            if not isinstance(tags, list) or not all(isinstance(tag, str) for tag in tags):
                raise ValueError("GHCR tags must be an array of strings or null")
            yield from tags

        next_urls = []
        for link in links:
            for target, attributes in re.findall(r"<([^>]+)>\s*([^,]*)", link):
                relation = re.search(r'(?:^|;)\s*rel\s*=\s*(?:"([^"]*)"|([^;\s]+))', attributes)
                if relation and "next" in (relation[1] or relation[2]).split():
                    next_urls.append(urljoin(url, target))
        if len(next_urls) > 1:
            raise ValueError("GHCR tag response contains multiple next pages")
        url = next_urls[0] if next_urls else None
        if url:
            location = urlsplit(url)
            if location.scheme != "https" or location.netloc != "ghcr.io" or location.path != tags_path:
                raise ValueError("GHCR tag pagination must stay on the repository tag endpoint")


def image_reference(repository, sha, username, password, released_at):
    repository = repository.lower()
    if not re.fullmatch(r"[0-9a-f]{40}", sha):
        raise ValueError("GITHUB_SHA must be a full lowercase commit hash")
    day = released_at.astimezone(timezone.utc).strftime("%y%m%d")
    pattern = re.compile(rf"{day}-([1-9][0-9]*)-[0-9a-f]{{8}}")
    sequence = 0
    for tag in registry_tags(repository, username, password):
        match = pattern.fullmatch(tag)
        if match:
            sequence = max(sequence, int(match[1]))
    return f"ghcr.io/{repository}:{day}-{sequence + 1}-{sha[:8]}"


def main():
    released_at = datetime.now(timezone.utc)
    reference = image_reference(
        os.environ["GITHUB_REPOSITORY"],
        os.environ["GITHUB_SHA"],
        os.environ["REGISTRY_USERNAME"],
        os.environ["REGISTRY_PASSWORD"],
        released_at,
    )
    print(f"reference={reference}")


if __name__ == "__main__":
    main()
