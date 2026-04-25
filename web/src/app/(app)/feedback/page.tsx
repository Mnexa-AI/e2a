"use client";

import { useState } from "react";
import { FEEDBACK_EMAIL } from "../../../lib/site";

export default function FeedbackPage() {
  const [email, setEmail] = useState("");
  const [category, setCategory] = useState<"bug" | "feature" | "general">("general");
  const [message, setMessage] = useState("");
  const [status, setStatus] = useState<"idle" | "sending" | "sent" | "error" | "rate-limited">("idle");

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!message.trim()) return;

    setStatus("sending");
    try {
      const res = await fetch("/api/feedback", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        credentials: "include",
        body: JSON.stringify({ email: email || undefined, category, message }),
      });
      if (res.ok) {
        setStatus("sent");
        setMessage("");
        setEmail("");
      } else if (res.status === 429) {
        setStatus("rate-limited");
      } else {
        setStatus("error");
      }
    } catch {
      setStatus("error");
    }
  };

  if (status === "sent") {
    return (
      <section className="max-w-xl py-12 text-center mx-auto">
        <div className="w-14 h-14 rounded-full bg-green-50 text-green-600 flex items-center justify-center text-2xl mx-auto mb-6">
          &#10003;
        </div>
        <h2 className="text-2xl font-bold tracking-tight mb-3">Thanks for your feedback</h2>
        <p className="text-muted mb-8">
          We read every submission and it directly shapes what we build next.
        </p>
        <button onClick={() => setStatus("idle")} className="text-sm text-accent hover:underline">
          Submit more feedback
        </button>
      </section>
    );
  }

  return (
    <section className="max-w-xl">
      <h2 className="text-2xl font-bold tracking-tight mb-2">Send us feedback</h2>
      <p className="text-muted mb-8">
        Bug reports, feature requests, or anything else.
        {FEEDBACK_EMAIL && (
          <>
            {" "}You can also reach us at{" "}
            <a href={`mailto:${FEEDBACK_EMAIL}`} className="text-accent hover:underline">{FEEDBACK_EMAIL}</a>.
          </>
        )}
      </p>

      <form onSubmit={handleSubmit} className="space-y-5">
        <div>
          <label htmlFor="feedback-email" className="block text-sm font-medium mb-1.5">
            Email <span className="text-muted font-normal">(optional, if you want a reply)</span>
          </label>
          <input
            id="feedback-email"
            type="email"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            placeholder="you@example.com"
            className="w-full border border-border rounded-lg px-4 py-2.5 text-sm bg-surface focus:outline-none focus:ring-2 focus:ring-accent/30 focus:border-accent transition"
          />
        </div>

        <div>
          <label className="block text-sm font-medium mb-1.5">Category</label>
          <div className="flex gap-2">
            {([
              { value: "bug", label: "Bug report" },
              { value: "feature", label: "Feature request" },
              { value: "general", label: "General" },
            ] as const).map((opt) => (
              <button
                key={opt.value}
                type="button"
                onClick={() => setCategory(opt.value)}
                className={`px-3 py-1.5 rounded-lg text-sm border transition ${
                  category === opt.value
                    ? "border-accent bg-accent/5 text-accent font-medium"
                    : "border-border text-muted hover:text-foreground hover:border-foreground/20"
                }`}
              >
                {opt.label}
              </button>
            ))}
          </div>
        </div>

        <div>
          <label htmlFor="feedback-message" className="block text-sm font-medium mb-1.5">Message</label>
          <textarea
            id="feedback-message"
            value={message}
            onChange={(e) => setMessage(e.target.value)}
            placeholder="What's on your mind?"
            rows={5}
            required
            className="w-full border border-border rounded-lg px-4 py-2.5 text-sm bg-surface focus:outline-none focus:ring-2 focus:ring-accent/30 focus:border-accent transition resize-none"
          />
        </div>

        <button
          type="submit"
          disabled={status === "sending" || !message.trim()}
          className="w-full px-6 py-3 bg-foreground text-background rounded-lg text-sm font-medium hover:opacity-90 transition disabled:opacity-50 disabled:cursor-not-allowed"
        >
          {status === "sending" ? "Sending..." : "Submit feedback"}
        </button>

        {status === "error" && (
          <p className="text-sm text-red-600 text-center">
            Something went wrong. Please try again or email us directly.
          </p>
        )}
        {status === "rate-limited" && (
          <p className="text-sm text-amber-600 text-center">
            Too many submissions. Please wait a minute before trying again.
          </p>
        )}
      </form>
    </section>
  );
}
