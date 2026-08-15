"""Usage normalization tests: cache tokens + reasoning across API shapes."""

from __future__ import annotations

from llm.openai_client import _normalize_openai_usage


def test_openai_nested_cached_tokens():
    u = _normalize_openai_usage({
        "prompt_tokens": 100,
        "completion_tokens": 20,
        "prompt_tokens_details": {"cached_tokens": 70},
    })
    assert u["input_tokens"] == 30  # 100 - 70 cached
    assert u["cache_read_input_tokens"] == 70
    assert u["output_tokens"] == 20


def test_deepseek_native_cache_tokens():
    u = _normalize_openai_usage({
        "prompt_tokens": 100,
        "completion_tokens": 20,
        "prompt_cache_hit_tokens": 60,
        "prompt_cache_miss_tokens": 40,
    })
    assert u["cache_read_input_tokens"] == 60
    assert u["cache_creation_input_tokens"] == 40
    assert u["input_tokens"] == 0  # 100 - 60 - 40


def test_anthropic_style_top_level_fallback():
    u = _normalize_openai_usage({
        "prompt_tokens": 100,
        "completion_tokens": 20,
        "cache_read_input_tokens": 50,
        "cache_creation_input_tokens": 30,
    })
    assert u["cache_read_input_tokens"] == 50
    assert u["cache_creation_input_tokens"] == 30
    assert u["input_tokens"] == 20


def test_reasoning_tokens_from_completion_details():
    u = _normalize_openai_usage({
        "prompt_tokens": 50,
        "completion_tokens": 100,
        "completion_tokens_details": {"reasoning_tokens": 90},
    })
    assert u["reasoning_tokens"] == 90


def test_empty_usage_returns_zeros():
    u = _normalize_openai_usage({})
    assert u["input_tokens"] == 0
    assert u["cache_read_input_tokens"] == 0
    assert u["reasoning_tokens"] == 0
