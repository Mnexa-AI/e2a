import { render, screen, waitFor } from "../../../../test-utils/swr";
import userEvent from "@testing-library/user-event";
import { PendingRow } from "./PendingRow";
import type { PendingMessageSummary } from "../../../components/types";

const AGENT = "support@acme.dev";
const summary: PendingMessageSummary = {
  id: "msg_1",
  agent_email: AGENT,
  direction: "outbound",
  subject: "Re: refund",
  to: ["customer@bigco.com"],
  status: "pending_review",
  created_at: new Date(Date.now() - 60_000).toISOString(),
};

// MessageView wire shape the detail fetch returns; projectPending reads
// review_status + body.
const detailWire = {
  message_id: "msg_1",
  from: AGENT,
  to: ["customer@bigco.com"],
  cc: [],
  recipient: "customer@bigco.com",
  subject: "Re: refund",
  conversation_id: "conv_1",
  review_status: "pending_review",
  created_at: summary.created_at,
  body: { text: "Hello, your refund is on the way.", html: "" },
};

const mockFetch = jest.fn();
beforeEach(() => {
  mockFetch.mockReset();
  global.fetch = mockFetch as unknown as typeof fetch;
});

// Detail + approve/reject now go through the account-scoped /v1/reviews
// resource (id is globally unique; no agent address in the path).
const detailURL = `/v1/reviews/msg_1`;
function stage(overrides: Record<string, unknown> = {}) {
  mockFetch.mockImplementation((url: string, init?: { method?: string }) => {
    if (url === `${detailURL}/approve` && init?.method === "POST")
      return Promise.resolve({ ok: true, status: 200, text: () => Promise.resolve(JSON.stringify({ status: "sent", message_id: "msg_1" })) });
    if (url === `${detailURL}/reject` && init?.method === "POST")
      return Promise.resolve({ ok: true, status: 200, text: () => Promise.resolve(JSON.stringify({ status: "review_rejected", message_id: "msg_1" })) });
    if (url === detailURL)
      return Promise.resolve({ ok: true, status: 200, text: () => Promise.resolve(JSON.stringify({ ...detailWire, ...overrides })) });
    return Promise.resolve({ ok: false, status: 404, text: () => Promise.resolve("nf") });
  });
}

describe("PendingRow", () => {
  it("annotates direction: outbound shows inbox → recipient + Outbound", () => {
    stage();
    render(<PendingRow summary={summary} expanded={false} onToggle={() => {}} onResolved={() => {}} />);
    expect(screen.getByText("Outbound")).toBeInTheDocument();
    expect(screen.getByText(/support@acme\.dev → customer@bigco\.com/)).toBeInTheDocument();
  });

  it("annotates direction: inbound shows sender → inbox + Inbound", () => {
    const inbound = {
      ...summary,
      direction: "inbound" as const,
      from: "suspicious@spammy.biz",
      to: [AGENT],
    };
    render(<PendingRow summary={inbound} expanded={false} onToggle={() => {}} onResolved={() => {}} />);
    expect(screen.getByText("Inbound")).toBeInTheDocument();
    expect(screen.getByText(/suspicious@spammy\.biz → support@acme\.dev/)).toBeInTheDocument();
  });

  it("inbound hold: release/block actions, no editor", async () => {
    stage();
    const inbound = {
      ...summary,
      direction: "inbound" as const,
      from: "suspicious@spammy.biz",
      to: [AGENT],
    };
    render(<PendingRow summary={inbound} expanded onToggle={() => {}} onResolved={() => {}} />);
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Approve & release" })).toBeInTheDocument(),
    );
    // No draft editing for an inbound (incoming) message.
    expect(screen.queryByRole("button", { name: "Edit draft" })).not.toBeInTheDocument();
  });

  it("collapsed shows the summary; not the body", () => {
    stage();
    render(<PendingRow summary={summary} expanded={false} onToggle={() => {}} onResolved={() => {}} />);
    expect(screen.getByText("Re: refund")).toBeInTheDocument();
    expect(screen.queryByText(/your refund is on the way/)).not.toBeInTheDocument();
  });

  it("expanded fetches + renders the body read-first with an action bar", async () => {
    stage();
    render(<PendingRow summary={summary} expanded onToggle={() => {}} onResolved={() => {}} />);
    await waitFor(() => expect(screen.getByText(/your refund is on the way/)).toBeInTheDocument());
    expect(screen.getByRole("button", { name: "Approve & send" })).toBeInTheDocument();
    // Editor is opt-in — no Subject input until Edit draft.
    expect(screen.queryByText("Subject")).not.toBeInTheDocument();
  });

  it("approve POSTs to /approve and calls onResolved", async () => {
    stage();
    const onResolved = jest.fn();
    const user = userEvent.setup();
    render(<PendingRow summary={summary} expanded onToggle={() => {}} onResolved={onResolved} />);
    await waitFor(() => screen.getByRole("button", { name: "Approve & send" }));
    await user.click(screen.getByRole("button", { name: "Approve & send" }));
    await waitFor(() => expect(onResolved).toHaveBeenCalled());
    expect(mockFetch).toHaveBeenCalledWith(`${detailURL}/approve`, expect.objectContaining({ method: "POST" }));
  });

  it("Edit draft reveals the editor and approve sends only changed fields", async () => {
    stage();
    const user = userEvent.setup();
    render(<PendingRow summary={summary} expanded onToggle={() => {}} onResolved={() => {}} />);
    await waitFor(() => screen.getByRole("button", { name: "Edit draft" }));
    await user.click(screen.getByRole("button", { name: "Edit draft" }));
    const subject = await screen.findByDisplayValue("Re: refund");
    await user.clear(subject);
    await user.type(subject, "Re: refund (approved)");
    await user.click(screen.getByRole("button", { name: "Approve & send edited" }));
    await waitFor(() =>
      expect(mockFetch).toHaveBeenCalledWith(`${detailURL}/approve`, expect.objectContaining({ method: "POST" })),
    );
    const call = mockFetch.mock.calls.find((c) => c[0] === `${detailURL}/approve`);
    const body = JSON.parse(call![1].body as string);
    expect(body.subject).toBe("Re: refund (approved)"); // only the changed field
    expect(body).not.toHaveProperty("body"); // body untouched → omitted
  });

  it("Reject reveals a reason field and POSTs to /reject", async () => {
    stage();
    const onResolved = jest.fn();
    const user = userEvent.setup();
    render(<PendingRow summary={summary} expanded onToggle={() => {}} onResolved={onResolved} />);
    await waitFor(() => screen.getByRole("button", { name: /^Reject/ }));
    await user.click(screen.getByRole("button", { name: /^Reject/ }));
    await user.type(screen.getByPlaceholderText("reject reason (optional)"), "tone off");
    await user.click(screen.getByRole("button", { name: "Reject draft" }));
    await waitFor(() => expect(onResolved).toHaveBeenCalled());
    const call = mockFetch.mock.calls.find((c) => c[0] === `${detailURL}/reject`);
    expect(JSON.parse(call![1].body as string).reason).toBe("tone off");
  });
});
