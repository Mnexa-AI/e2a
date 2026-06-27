import Link from "next/link";
import { Eyebrow } from "@e2a/ui";
import { getPostsSortedByDate } from "./posts";

function formatDate(iso: string): string {
  const d = new Date(iso + "T00:00:00Z");
  return d.toLocaleDateString("en-US", {
    month: "long",
    day: "numeric",
    year: "numeric",
    timeZone: "UTC",
  });
}

export default function BlogIndex() {
  const posts = getPostsSortedByDate();
  return (
    <div>
      <header className="mb-10">
        <Eyebrow>Blog</Eyebrow>
        <h1
          className="mt-3 mb-3"
          style={{
            fontFamily: "var(--f-editorial)",
            fontWeight: 400,
            fontSize: "clamp(32px, 5vw, 42px)",
            letterSpacing: "-0.012em",
            color: "var(--fg)",
            lineHeight: 1.1,
          }}
        >
          Notes on email, agents, and the space{" "}
          <em style={{ color: "var(--accent-strong)" }}>in between.</em>
        </h1>
        <p
          className="text-[14px] leading-[1.6]"
          style={{ color: "var(--fg-muted)" }}
        >
          Tutorials, protocol deep-dives, and product updates from the team.
        </p>
      </header>
      <div className="flex flex-col">
        {posts.map((post) => (
          <Link
            key={post.slug}
            href={`/blog/${post.slug}`}
            className="block py-6"
            style={{
              borderBottom: "1px solid var(--border)",
              color: "inherit",
            }}
          >
            <div
              className="font-mono text-[11px] mb-1.5"
              style={{ color: "var(--fg-subtle)" }}
            >
              {formatDate(post.date)} · {post.readingMinutes} min read
            </div>
            <h2
              className="text-[20px] font-medium mb-1.5 leading-[1.3]"
              style={{ color: "var(--fg)" }}
            >
              {post.title}
            </h2>
            <p
              className="text-[14px] leading-[1.6]"
              style={{ color: "var(--fg-muted)" }}
            >
              {post.description}
            </p>
          </Link>
        ))}
      </div>
    </div>
  );
}
