import unittest

import pytest

from agent_webhooks.delivery_state import EventDeduper


class EventDeduperTest(unittest.IsolatedAsyncioTestCase):
    async def test_claim_release_and_complete_transitions(self) -> None:
        deduper = EventDeduper(max_processed=1)

        self.assertEqual(await deduper.claim("evt_1"), "new")
        self.assertEqual(await deduper.claim("evt_1"), "processing")

        await deduper.release("evt_1")
        self.assertEqual(await deduper.claim("evt_1"), "new")

        await deduper.complete("evt_1")
        self.assertEqual(await deduper.claim("evt_1"), "processed")

    async def test_completed_event_cache_evicts_oldest_event(self) -> None:
        deduper = EventDeduper(max_processed=1)

        self.assertEqual(await deduper.claim("evt_old"), "new")
        await deduper.complete("evt_old")
        self.assertEqual(await deduper.claim("evt_new"), "new")
        await deduper.complete("evt_new")

        self.assertEqual(await deduper.claim("evt_old"), "new")
        self.assertEqual(await deduper.claim("evt_new"), "processed")


@pytest.mark.parametrize("max_processed", [0, -1])
def test_nonpositive_capacity_is_rejected(max_processed: int) -> None:
    with pytest.raises(ValueError, match="max_processed must be positive"):
        EventDeduper(max_processed=max_processed)
