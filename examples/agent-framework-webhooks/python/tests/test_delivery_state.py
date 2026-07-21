import asyncio
import unittest

import pytest

from agent_webhooks.delivery_state import EventDeduper, conversation_id_for


class ConversationIDTest(unittest.TestCase):
    def test_uses_the_full_event_suffix_without_collisions(self) -> None:
        first = conversation_id_for("evt_0123456789ab_one", None)
        second = conversation_id_for("evt_0123456789ab_two", None)

        self.assertEqual(first, "conv_0123456789ab_one")
        self.assertEqual(second, "conv_0123456789ab_two")
        self.assertNotEqual(first, second)

    def test_treats_whitespace_only_existing_id_as_missing(self) -> None:
        self.assertEqual(conversation_id_for("evt_full_suffix", "  \t"), "conv_full_suffix")

    def test_hashes_unsafe_or_oversized_event_ids_within_api_cap(self) -> None:
        unsafe = conversation_id_for("evt_bad\r\nsuffix", None)
        oversized = conversation_id_for(f"evt_{'a' * 300}", None)

        self.assertRegex(unsafe, r"^conv_[0-9a-f]{64}$")
        self.assertRegex(oversized, r"^conv_[0-9a-f]{64}$")
        self.assertLessEqual(len(oversized), 200)
        self.assertNotEqual(unsafe, oversized)


class EventDeduperTest(unittest.IsolatedAsyncioTestCase):
    async def test_concurrent_claims_have_exactly_one_winner(self) -> None:
        deduper = EventDeduper()

        results = await asyncio.gather(
            *(deduper.claim("evt_contended") for _ in range(100))
        )

        self.assertEqual(results.count("new"), 1)
        self.assertEqual(results.count("processing"), 99)

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
