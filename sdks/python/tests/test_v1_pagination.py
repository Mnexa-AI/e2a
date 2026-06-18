"""Unit tests for the async AutoPager (Slice 8c-1)."""

import pytest

from e2a.v1.pagination import AutoPager, Page


def make_pages(data):
    async def fetch(cursor):
        return data[cursor if cursor is not None else "__start__"]

    return fetch


THREE_PAGES = {
    "__start__": Page(items=[1, 2], next_cursor="c2"),
    "c2": Page(items=[3, 4], next_cursor="c3"),
    "c3": Page(items=[5], next_cursor=None),
}


@pytest.mark.anyio
async def test_iterates_all_items_until_null_cursor():
    pager = AutoPager(make_pages(THREE_PAGES))
    out = [n async for n in pager]
    assert out == [1, 2, 3, 4, 5]


@pytest.mark.anyio
async def test_to_list_respects_limit():
    pager = AutoPager(make_pages(THREE_PAGES))
    assert await pager.to_list(limit=3) == [1, 2, 3]


@pytest.mark.anyio
async def test_to_list_requires_positive_limit():
    pager = AutoPager(make_pages(THREE_PAGES))
    with pytest.raises(ValueError, match="positive limit"):
        await pager.to_list(limit=0)


@pytest.mark.anyio
async def test_for_each_stops_on_false():
    pager = AutoPager(make_pages(THREE_PAGES))
    out = []

    async def collect(n):
        out.append(n)
        return n < 3

    await pager.for_each(collect)
    assert out == [1, 2, 3]


@pytest.mark.anyio
async def test_empty_string_cursor_terminates():
    pager = AutoPager(lambda c: _single(Page(items=[7], next_cursor="")))
    assert await pager.to_list(limit=100) == [7]


def _single(page):
    async def fetch(_cursor):
        return page

    return fetch(None)


@pytest.mark.anyio
async def test_non_advancing_cursor_aborts():
    calls = {"n": 0}

    async def fetch(_cursor):
        calls["n"] += 1
        return Page(items=[calls["n"]], next_cursor="stuck")

    pager = AutoPager(fetch)
    with pytest.raises(RuntimeError, match="did not advance"):
        async for _ in pager:
            if calls["n"] > 10:
                raise AssertionError("looped")
    assert calls["n"] <= 2


@pytest.mark.anyio
async def test_multi_step_cycle_aborts():
    ring = ["c1", "c2", "c3"]
    i = {"n": 0}

    async def fetch(_cursor):
        nxt = ring[i["n"] % len(ring)]
        i["n"] += 1
        return Page(items=[i["n"]], next_cursor=nxt)

    pager = AutoPager(fetch)
    with pytest.raises(RuntimeError, match="did not advance"):
        async for _ in pager:
            if i["n"] > 20:
                raise AssertionError("looped")
    assert i["n"] <= 5


@pytest.mark.anyio
async def test_ever_advancing_cursor_hits_max_pages():
    n = {"v": 0}

    async def fetch(_cursor):
        n["v"] += 1
        return Page(items=[n["v"]], next_cursor=f"c{n['v']}")

    pager = AutoPager(fetch, max_pages=5)
    with pytest.raises(RuntimeError, match="exceeded 5 pages"):
        async for _ in pager:
            pass
