import Link from "next/link";
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
      <header style={{ marginBottom: 40 }}>
        <p
          style={{
            fontSize: 11,
            fontWeight: 500,
            letterSpacing: "0.07em",
            textTransform: "uppercase",
            color: "#A89A8A",
            marginBottom: 8,
          }}
        >
          Blog
        </p>
        <h1
          style={{
            fontFamily: "'Instrument Serif', Georgia, serif",
            fontSize: 40,
            fontWeight: 400,
            color: "#1C1A17",
            margin: "0 0 10px",
            letterSpacing: "-0.01em",
          }}
        >
          Notes on email, agents, and the space in between.
        </h1>
        <p style={{ fontSize: 14, color: "#7A6F63", lineHeight: 1.6 }}>
          Tutorials, protocol deep-dives, and product updates from the team.
        </p>
      </header>
      <div style={{ display: "flex", flexDirection: "column", gap: 0 }}>
        {posts.map((post) => (
          <Link
            key={post.slug}
            href={`/blog/${post.slug}`}
            style={{
              display: "block",
              textDecoration: "none",
              color: "inherit",
              padding: "22px 0",
              borderBottom: "0.5px solid #E8E0D4",
            }}
          >
            <div
              style={{
                fontSize: 11,
                color: "#A89A8A",
                marginBottom: 6,
                fontFamily: "'IBM Plex Mono', monospace",
              }}
            >
              {formatDate(post.date)} · {post.readingMinutes} min read
            </div>
            <h2
              style={{
                fontSize: 20,
                fontWeight: 500,
                color: "#1C1A17",
                margin: "0 0 6px",
                lineHeight: 1.3,
              }}
            >
              {post.title}
            </h2>
            <p style={{ fontSize: 14, color: "#7A6F63", lineHeight: 1.6, margin: 0 }}>
              {post.description}
            </p>
          </Link>
        ))}
      </div>
    </div>
  );
}
