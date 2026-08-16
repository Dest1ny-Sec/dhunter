"""Agent API auth tests: the /v1 sidecar routes require the same bearer
token as the platform server (three-process one-standard)."""

from __future__ import annotations

import pytest
from fastapi import Depends, FastAPI
from fastapi.testclient import TestClient

import core.server as server_mod


@pytest.fixture
def authed_app(monkeypatch):
    monkeypatch.setattr(server_mod, "_AUTH_TOKEN", "test-agent-token")
    app = FastAPI()

    @app.get("/v1/probe", dependencies=[Depends(server_mod.require_token)])
    def probe():
        return {"ok": True}

    return TestClient(app)


def test_unauthenticated_v1_call_rejected(authed_app):
    resp = authed_app.get("/v1/probe")
    assert resp.status_code == 401


def test_wrong_token_rejected(authed_app):
    resp = authed_app.get("/v1/probe", headers={"Authorization": "Bearer nope"})
    assert resp.status_code == 401


def test_correct_token_accepted(authed_app):
    resp = authed_app.get("/v1/probe", headers={"Authorization": "Bearer test-agent-token"})
    assert resp.status_code == 200
    assert resp.json() == {"ok": True}


def test_auth_disabled_without_configured_token(monkeypatch):
    """No DHUNTER_AGENT_TOKEN configured → auth is off (explicit local-dev
    opt-out, logged loudly at startup)."""
    monkeypatch.setattr(server_mod, "_AUTH_TOKEN", "")
    app = FastAPI()

    @app.get("/v1/probe", dependencies=[Depends(server_mod.require_token)])
    def probe():
        return {"ok": True}

    resp = TestClient(app).get("/v1/probe")
    assert resp.status_code == 200
