from pathlib import Path
import unittest


ROOT = Path(__file__).parent


def source(framework: str, filename: str = "agent.py") -> str:
    return (ROOT / framework / filename).read_text()


class FrameworkExampleContractTest(unittest.TestCase):
    def test_all_frameworks_support_self_hosted_mcp_and_email_safety(self) -> None:
        for framework in ("langchain", "crewai", "openai-agents"):
            with self.subTest(framework=framework):
                agent = source(framework)
                self.assertIn("E2A_MCP_URL", agent)
                self.assertIn("pending_review", agent)
                self.assertIn("Do not retry", agent)
                self.assertIn("agent-scoped", agent)

    def test_langchain_uses_current_agent_and_http_transport(self) -> None:
        agent = source("langchain")
        requirements = source("langchain", "requirements.txt")

        self.assertIn("from langchain.agents import create_agent", agent)
        self.assertNotIn("langgraph.prebuilt", agent)
        self.assertIn('"transport": "http"', agent)
        self.assertIn("langchain-mcp-adapters>=0.3", requirements)
        self.assertIn("langchain>=1", requirements)

    def test_crewai_uses_current_native_mcp_configuration(self) -> None:
        agent = source("crewai")
        requirements = source("crewai", "requirements.txt")

        self.assertIn("from crewai.mcp import MCPServerHTTP", agent)
        self.assertIn("mcps=[e2a]", agent)
        self.assertNotIn("MCPServerAdapter", agent)
        self.assertIn("crewai[anthropic]>=1.13", requirements)
        self.assertNotIn("crewai-tools", requirements)

    def test_openai_agents_uses_current_remote_mcp_resilience_options(self) -> None:
        agent = source("openai-agents")
        requirements = source("openai-agents", "requirements.txt")

        self.assertIn("cache_tools_list=True", agent)
        self.assertIn("max_retry_attempts=3", agent)
        self.assertIn("openai-agents>=0.13", requirements)


if __name__ == "__main__":
    unittest.main()
