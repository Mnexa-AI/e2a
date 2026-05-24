import { formatRelativeAge } from "./relativeTime";

describe("formatRelativeAge", () => {
  const NOW = new Date("2026-05-24T12:00:00Z");

  it("renders '—' for null / undefined / invalid input", () => {
    expect(formatRelativeAge(null, NOW)).toBe("—");
    expect(formatRelativeAge(undefined, NOW)).toBe("—");
    expect(formatRelativeAge("", NOW)).toBe("—");
    expect(formatRelativeAge("not a date", NOW)).toBe("—");
  });

  it("future timestamps render '—' (caller's clock is ahead)", () => {
    const future = new Date(NOW.getTime() + 60_000).toISOString();
    expect(formatRelativeAge(future, NOW)).toBe("—");
  });

  it("under 60s reads 'just now'", () => {
    const recent = new Date(NOW.getTime() - 5_000).toISOString();
    expect(formatRelativeAge(recent, NOW)).toBe("just now");
  });

  it("minutes/hours/days format with single-letter unit", () => {
    expect(
      formatRelativeAge(new Date(NOW.getTime() - 7 * 60_000).toISOString(), NOW),
    ).toBe("7m ago");
    expect(
      formatRelativeAge(new Date(NOW.getTime() - 3 * 60 * 60_000).toISOString(), NOW),
    ).toBe("3h ago");
    expect(
      formatRelativeAge(
        new Date(NOW.getTime() - 5 * 24 * 60 * 60_000).toISOString(),
        NOW,
      ),
    ).toBe("5d ago");
  });

  it("boundary at 60 minutes rolls over to hours, not stuck at 60m", () => {
    const exactlyOneHour = new Date(NOW.getTime() - 60 * 60_000).toISOString();
    expect(formatRelativeAge(exactlyOneHour, NOW)).toBe("1h ago");
  });
});
