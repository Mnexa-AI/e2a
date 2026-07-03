import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { AgentPromptCard, AGENT_PROMPTS } from "./AgentPromptCard";

// jsdom doesn't provide navigator.clipboard. The Copy-prompt button calls
// writeText, so we install a jest mock once at module level (same pattern
// as the settings page tests).
const writeText = jest.fn(async () => {});
Object.assign(navigator, { clipboard: { writeText } });
beforeEach(() => writeText.mockClear());

test("renders the blurb and prompt verbatim", () => {
  render(<AgentPromptCard {...AGENT_PROMPTS.templates} />);
  expect(
    screen.getByRole("heading", { name: "Set up with a coding agent" }),
  ).toBeInTheDocument();
  expect(screen.getByText(AGENT_PROMPTS.templates.blurb)).toBeInTheDocument();
  expect(screen.getByText(AGENT_PROMPTS.templates.prompt)).toBeInTheDocument();
});

test("every prompt anchors on a published e2a.dev doc, not a GitHub path", () => {
  for (const { prompt } of Object.values(AGENT_PROMPTS)) {
    expect(prompt).toMatch(/https:\/\/e2a\.dev\/(templates|e2a)\.md/);
    expect(prompt).not.toContain("github.com");
  }
});

test("copy button writes the card's prompt to the clipboard and flips its label", async () => {
  render(<AgentPromptCard {...AGENT_PROMPTS.domains} />);
  fireEvent.click(screen.getByRole("button", { name: "Copy prompt" }));
  await waitFor(() =>
    expect(writeText).toHaveBeenCalledWith(AGENT_PROMPTS.domains.prompt),
  );
  await waitFor(() =>
    expect(
      screen.getByRole("button", { name: "Copy prompt" }),
    ).toHaveTextContent("Copied"),
  );
});
