import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import FeedbackPage from "./page";

const mockFetch = jest.fn();
global.fetch = mockFetch;

beforeEach(() => {
  mockFetch.mockReset();
});

describe("Feedback page", () => {
  it("renders form with all elements", () => {
    render(<FeedbackPage />);
    expect(screen.getByText("Send us feedback")).toBeInTheDocument();
    expect(screen.getByPlaceholderText("you@example.com")).toBeInTheDocument();
    expect(screen.getByPlaceholderText("What's on your mind?")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Bug report" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Feature request" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "General" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Submit feedback" })).toBeDisabled();
  });

  it("enables submit when message is entered", async () => {
    const user = userEvent.setup();
    render(<FeedbackPage />);
    await user.type(screen.getByPlaceholderText("What's on your mind?"), "Great product!");
    expect(screen.getByRole("button", { name: "Submit feedback" })).toBeEnabled();
  });

  it("submits feedback successfully and shows thank you", async () => {
    mockFetch.mockResolvedValueOnce({ ok: true, json: async () => ({ status: "ok" }) });
    const user = userEvent.setup();
    render(<FeedbackPage />);
    await user.type(screen.getByPlaceholderText("What's on your mind?"), "Love it!");
    await user.click(screen.getByRole("button", { name: "Submit feedback" }));
    await waitFor(() => {
      expect(screen.getByText("Thanks for your feedback")).toBeInTheDocument();
    });
  });

  it("shows error message on failed submission", async () => {
    mockFetch.mockResolvedValueOnce({ ok: false, status: 500, text: async () => "server error" });
    const user = userEvent.setup();
    render(<FeedbackPage />);
    await user.type(screen.getByPlaceholderText("What's on your mind?"), "Test error");
    await user.click(screen.getByRole("button", { name: "Submit feedback" }));
    await waitFor(() => {
      expect(screen.getByText("Something went wrong. Please try again or email us directly.")).toBeInTheDocument();
    });
  });

  it("shows rate-limited message on 429 response", async () => {
    mockFetch.mockResolvedValueOnce({ ok: false, status: 429, text: async () => "rate limited" });
    const user = userEvent.setup();
    render(<FeedbackPage />);
    await user.type(screen.getByPlaceholderText("What's on your mind?"), "Too many");
    await user.click(screen.getByRole("button", { name: "Submit feedback" }));
    await waitFor(() => {
      expect(screen.getByText("Too many submissions. Please wait a minute before trying again.")).toBeInTheDocument();
    });
  });

  it("allows submitting more feedback after success", async () => {
    mockFetch.mockResolvedValueOnce({ ok: true, json: async () => ({ status: "ok" }) });
    const user = userEvent.setup();
    render(<FeedbackPage />);
    await user.type(screen.getByPlaceholderText("What's on your mind?"), "First feedback");
    await user.click(screen.getByRole("button", { name: "Submit feedback" }));
    await waitFor(() => {
      expect(screen.getByText("Thanks for your feedback")).toBeInTheDocument();
    });
    await user.click(screen.getByText("Submit more feedback"));
    await waitFor(() => {
      expect(screen.getByText("Send us feedback")).toBeInTheDocument();
    });
  });

  it("selects different category", async () => {
    const user = userEvent.setup();
    render(<FeedbackPage />);
    const bugBtn = screen.getByRole("button", { name: "Bug report" });
    await user.click(bugBtn);
    expect(bugBtn.className).toContain("border-accent");
  });
});
