import { render, screen, waitFor } from "../../../../../test-utils/swr";
import userEvent from "@testing-library/user-event";
import { PendingDetailPanel } from "./PendingDetailPanel";

// Mock fetch with a controllable jest.fn that records every call. The
// panel calls /api/v1/messages/{id} on mount and /api/v1/messages/{id}/
// approve on submit; both go through the api.ts helpers which delegate
// to global fetch.
const mockFetch = jest.fn();
beforeEach(() => {
  mockFetch.mockReset();
  global.fetch = mockFetch;
});

const baseMessage = {
  id: "msg_abc",
  agent_id: "bot@acme.io",
  direction: "outbound",
  subject: "original subject",
  type: "reply",
  to: ["alice@example.com"],
  cc: [],
  bcc: [],
  status: "pending_approval",
  approval_expires_at: "2099-01-01T00:00:00Z",
  created_at: "2026-05-23T00:00:00Z",
  body_text: "original body",
  body_html: "",
};

// Stage the GET /messages/{id} mock and the POST /messages/{id}/approve
// mock. Returns the spy so the caller can inspect the approve body.
function stagePanelFetch(message = baseMessage) {
  mockFetch.mockImplementation(
    (url: string, init?: { method?: string; body?: string }) => {
      if (url === `/api/v1/agents/${encodeURIComponent(message.agent_id)}/messages/${message.id}/approve` && init?.method === "POST") {
        return Promise.resolve({
          ok: true,
          json: () =>
            Promise.resolve({ status: "sent", message_id: message.id }),
        });
      }
      if (url === `/api/v1/messages/${message.id}`) {
        return Promise.resolve({
          ok: true,
          json: () => Promise.resolve(message),
        });
      }
      return Promise.resolve({
        ok: false,
        status: 404,
        text: () => Promise.resolve("not found"),
      });
    },
  );
}

describe("PendingDetailPanel", () => {
  it("posts only edited fields when reviewer changes subject before approving", async () => {
    stagePanelFetch();
    const onChanged = jest.fn();
    const user = userEvent.setup();

    render(<PendingDetailPanel messageId="msg_abc" onChanged={onChanged} />);

    // Wait for load — subject input should have the original value
    const subject = await screen.findByDisplayValue("original subject");

    // Inputs are read-only until reviewer clicks "Edit draft"
    await user.click(screen.getByRole("button", { name: /edit draft/i }));

    // Edit the subject
    await user.clear(subject);
    await user.type(subject, "edited subject");

    // Click Approve & send
    await user.click(screen.getByRole("button", { name: /approve & send/i }));

    // The approve POST must have fired with ONLY the changed field in
    // the body. body_text, to, cc, bcc weren't touched so they must
    // NOT appear in the payload (lest we accidentally overwrite the
    // agent's draft with empty strings).
    await waitFor(() => {
      const approveCall = mockFetch.mock.calls.find(
        (call) =>
          call[0] === "/api/v1/agents/bot%40acme.io/messages/msg_abc/approve" &&
          call[1]?.method === "POST",
      );
      expect(approveCall).toBeDefined();
      const body = JSON.parse(approveCall![1].body);
      expect(body).toEqual({ subject: "edited subject" });
    });

    await waitFor(() => expect(onChanged).toHaveBeenCalled());
  });

  it("sends an empty body when reviewer approves without editing", async () => {
    stagePanelFetch();
    const onChanged = jest.fn();
    const user = userEvent.setup();

    render(<PendingDetailPanel messageId="msg_abc" onChanged={onChanged} />);
    await screen.findByDisplayValue("original subject");

    await user.click(screen.getByRole("button", { name: /approve & send/i }));

    await waitFor(() => {
      const approveCall = mockFetch.mock.calls.find(
        (call) =>
          call[0] === "/api/v1/agents/bot%40acme.io/messages/msg_abc/approve" &&
          call[1]?.method === "POST",
      );
      expect(approveCall).toBeDefined();
      // No-edits = empty object — the server treats absent fields as
      // "use original draft". Sending a non-empty body would risk
      // overwriting the agent's content with the loaded form state.
      expect(JSON.parse(approveCall![1].body)).toEqual({});
    });
  });

  it("disables the form when the message is no longer pending", async () => {
    stagePanelFetch({ ...baseMessage, status: "sent" });

    render(<PendingDetailPanel messageId="msg_abc" onChanged={() => {}} />);

    const subject = await screen.findByDisplayValue("original subject");
    expect(subject).toBeDisabled();
    // Approve / Reject buttons hidden on terminal status
    expect(
      screen.queryByRole("button", { name: /approve & send/i }),
    ).not.toBeInTheDocument();
  });
});
