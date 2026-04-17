from __future__ import annotations

import json
from dataclasses import dataclass
from pathlib import Path
from typing import Any
from urllib.parse import urlencode

import httpx

from ..runtime.interpolation import Interpolator
from ..validation.selector import is_supported_jsonpath


@dataclass(slots=True)
class PreparedRequest:
    method: str
    url: str
    headers: dict[str, Any]
    content: bytes | str | None = None


class RequestPreparer:
    def __init__(self, interpolator: Interpolator | None = None) -> None:
        self._interpolator = interpolator or Interpolator()

    def prepare(self, request, lookup: dict[str, Any], *, project_root: Path | None = None) -> PreparedRequest:
        resolved = self._interpolator.resolve_data({
            "method": request.method,
            "url": request.url,
            "baseUrl": request.base_url,
            "path": request.path,
            "pathParams": request.path_params,
            "query": request.query,
            "headers": request.headers,
            "body": request.body,
            "bodyRaw": request.body_raw,
        }, lookup)
        url = self._build_url(resolved)
        headers = dict(resolved.get("headers") or {})
        content = None
        if resolved.get("body") is not None:
            content = json.dumps(resolved["body"])
            headers.setdefault("Content-Type", "application/json")
        elif resolved.get("bodyRaw") is not None:
            content = resolved["bodyRaw"]
        return PreparedRequest(method=str(resolved["method"]).upper(), url=url, headers=headers, content=content)

    def _build_url(self, resolved: dict[str, Any]) -> str:
        if resolved.get("url"):
            url = str(resolved["url"])
        else:
            path = str(resolved.get("path") or "")
            for key, value in (resolved.get("pathParams") or {}).items():
                path = path.replace("{" + key + "}", str(value))
            url = str(resolved["baseUrl"]) + path
        query = resolved.get("query") or {}
        if query:
            url += ("&" if "?" in url else "?") + urlencode(query, doseq=False)
        return url


class HttpExecutor:
    def __init__(self, client: httpx.AsyncClient | None = None) -> None:
        self._client = client

    async def send(self, prepared: PreparedRequest, *, timeout_ms: int | None = None, follow_redirects: bool | None = None) -> httpx.Response:
        if self._client is not None:
            return await self._client.request(prepared.method, prepared.url, headers=prepared.headers, content=prepared.content, timeout=(timeout_ms / 1000.0) if timeout_ms else None, follow_redirects=follow_redirects)
        async with httpx.AsyncClient(http2=True) as client:
            return await client.request(prepared.method, prepared.url, headers=prepared.headers, content=prepared.content, timeout=(timeout_ms / 1000.0) if timeout_ms else None, follow_redirects=follow_redirects)
