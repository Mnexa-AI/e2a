import ast
from pathlib import Path
import unittest

from delivery_state import EventDeduper, conversation_id_for, sender_user_id


class ConversationIDTest(unittest.TestCase):
    def test_preserves_an_existing_conversation_id(self) -> None:
        self.assertEqual(conversation_id_for("evt_1", "conv_existing"), "conv_existing")

    def test_first_contact_id_is_deterministic_for_retries(self) -> None:
        first = conversation_id_for("evt_0123456789abcdef", None)
        second = conversation_id_for("evt_0123456789abcdef", None)

        self.assertEqual(first, second)
        self.assertEqual(first, "conv_0123456789ab")


class SenderUserIDTest(unittest.TestCase):
    def test_uses_header_from(self) -> None:
        self.assertEqual(sender_user_id("Alice@Example.com", "msg_1"), "Alice@Example.com")

    def test_missing_header_from_is_isolated_per_message(self) -> None:
        self.assertEqual(sender_user_id(None, "msg_1"), "unknown-sender:msg_1")
        self.assertNotEqual(sender_user_id(None, "msg_1"), sender_user_id(None, "msg_2"))


class EventDeduperTest(unittest.IsolatedAsyncioTestCase):
    async def test_claim_distinguishes_new_processing_and_completed_events(self) -> None:
        deduper = EventDeduper()

        self.assertEqual(await deduper.claim("evt_1"), "new")
        self.assertEqual(await deduper.claim("evt_1"), "processing")

        await deduper.complete("evt_1")
        self.assertEqual(await deduper.claim("evt_1"), "processed")

    async def test_release_allows_a_failed_event_to_retry(self) -> None:
        deduper = EventDeduper()

        self.assertEqual(await deduper.claim("evt_2"), "new")
        await deduper.release("evt_2")

        self.assertEqual(await deduper.claim("evt_2"), "new")

    async def test_completed_event_cache_is_bounded(self) -> None:
        deduper = EventDeduper(max_processed=1)

        self.assertEqual(await deduper.claim("evt_old"), "new")
        await deduper.complete("evt_old")
        self.assertEqual(await deduper.claim("evt_new"), "new")
        await deduper.complete("evt_new")

        self.assertEqual(await deduper.claim("evt_old"), "new")


class WebhookExampleContractTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.source = Path(__file__).with_name("webhook.py").read_text()
        cls.tree = ast.parse(cls.source)

    def test_current_event_sender_field_is_used(self) -> None:
        self.assertIn('data.get("header_from")', self.source)
        self.assertNotIn('data["from"]', self.source)

    def test_reply_uses_event_id_as_idempotency_key(self) -> None:
        reply_calls = [
            node
            for node in ast.walk(self.tree)
            if isinstance(node, ast.Call)
            and isinstance(node.func, ast.Attribute)
            and node.func.attr == "reply"
        ]
        self.assertEqual(len(reply_calls), 1)
        keyword = next(
            (item for item in reply_calls[0].keywords if item.arg == "idempotency_key"),
            None,
        )
        self.assertIsNotNone(keyword)
        self.assertEqual(ast.unparse(keyword.value), "event.id")

    def test_lifespan_closes_the_async_client(self) -> None:
        awaited_calls = [
            node.value
            for node in ast.walk(self.tree)
            if isinstance(node, ast.Await) and isinstance(node.value, ast.Call)
        ]
        self.assertTrue(
            any(
                isinstance(call.func, ast.Attribute)
                and call.func.attr == "aclose"
                for call in awaited_calls
            )
        )


if __name__ == "__main__":
    unittest.main()
