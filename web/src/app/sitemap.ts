import type { MetadataRoute } from "next";
import { posts } from "./blog/posts";
import { SITE_URL } from "../lib/site";

// Required for static export (output: "export")
export const dynamic = "force-static";

export default function sitemap(): MetadataRoute.Sitemap {
  const now = new Date();
  const staticRoutes: Array<{ path: string; changeFrequency: "daily" | "weekly" | "monthly"; priority: number }> = [
    { path: "/", changeFrequency: "weekly", priority: 1.0 },
    { path: "/docs", changeFrequency: "weekly", priority: 0.8 },
    { path: "/docs/python", changeFrequency: "weekly", priority: 0.8 },
    { path: "/python-sdk", changeFrequency: "weekly", priority: 0.7 },
    { path: "/api-docs", changeFrequency: "weekly", priority: 0.7 },
    { path: "/blog", changeFrequency: "weekly", priority: 0.7 },
    { path: "/get-started", changeFrequency: "monthly", priority: 0.6 },
  ];
  const blogRoutes = posts.map((p) => ({
    path: `/blog/${p.slug}`,
    changeFrequency: "monthly" as const,
    priority: 0.6,
    lastModified: new Date(p.date + "T00:00:00Z"),
  }));
  return [
    ...staticRoutes.map((r) => ({
      url: `${SITE_URL}${r.path}`,
      lastModified: now,
      changeFrequency: r.changeFrequency,
      priority: r.priority,
    })),
    ...blogRoutes.map((r) => ({
      url: `${SITE_URL}${r.path}`,
      lastModified: r.lastModified,
      changeFrequency: r.changeFrequency,
      priority: r.priority,
    })),
  ];
}
