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
        self.assertEqual(first, "conv_0123456789abcdef")

    def test_full_suffix_avoids_shared_prefix_collisions(self) -> None:
        first = conversation_id_for("evt_0123456789ab_one", None)
        second = conversation_id_for("evt_0123456789ab_two", None)

        self.assertNotEqual(first, second)

    def test_whitespace_only_existing_id_is_missing(self) -> None:
        self.assertEqual(conversation_id_for("evt_full_suffix", " \t"), "conv_full_suffix")

    def test_unsafe_or_oversized_event_id_uses_safe_digest(self) -> None:
        unsafe = conversation_id_for("evt_bad\r\nsuffix", None)
        oversized = conversation_id_for(f"evt_{'a' * 300}", None)

        self.assertRegex(unsafe, r"^conv_[0-9a-f]{64}$")
        self.assertRegex(oversized, r"^conv_[0-9a-f]{64}$")
        self.assertLessEqual(len(oversized), 200)


class SenderUserIDTest(unittest.TestCase):
    def test_display_name_and_case_variants_share_one_private_id(self) -> None:
        first = sender_user_id(
            "Alice Example <Alice@Example.com>", "Bot@Agents.E2A.dev", "msg_1"
        )
        second = sender_user_id(
            "alice@example.COM", "bot@agents.e2a.dev", "msg_2"
        )

        self.assertEqual(first, second)
        self.assertTrue(first.startswith("sender_"))
        self.assertNotIn("alice", first)

    def test_same_sender_is_isolated_across_inboxes(self) -> None:
        first = sender_user_id("alice@example.com", "one@agents.e2a.dev", "msg_1")
        second = sender_user_id("alice@example.com", "two@agents.e2a.dev", "msg_1")

        self.assertNotEqual(first, second)

    def test_missing_header_from_is_isolated_per_message(self) -> None:
        first = sender_user_id(None, "bot@agents.e2a.dev", "msg_1")
        second = sender_user_id(None, "bot@agents.e2a.dev", "msg_2")

        self.assertNotEqual(first, second)
        self.assertNotIn("msg_1", first)


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
    source: str
    tree: ast.Module

    @classmethod
    def setUpClass(cls) -> None:
        cls.source = Path(__file__).with_name("webhook.py").read_text()
        cls.tree = ast.parse(cls.source)

    def test_hydrated_sender_field_is_used(self) -> None:
        self.assertIn("email.from_", self.source)
        self.assertNotIn('data["from"]', self.source)

    def test_uses_the_ergonomic_inbound_facade(self) -> None:
        self.assertIn("await client.inbound.from_event(event)", self.source)
        self.assertIn("await email.reply(", self.source)
        self.assertNotIn("client.webhooks.fetch_message", self.source)
        self.assertNotIn("client.messages.reply", self.source)

    def test_prompt_uses_safe_normalized_email_fields(self) -> None:
        self.assertIn("_format_email_for_agent(email)", self.source)
        self.assertIn("email.from_", self.source)
        self.assertIn("email.subject", self.source)
        self.assertIn("email.text", self.source)
        self.assertNotIn("raw_message", self.source)

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
        assert keyword is not None
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
