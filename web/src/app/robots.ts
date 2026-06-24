import type { MetadataRoute } from "next";
import { SITE_URL } from "../lib/site";

// Required for static export (output: "export")
export const dynamic = "force-static";

export default function robots(): MetadataRoute.Robots {
  return {
    rules: [
      {
        userAgent: "*",
        allow: "/",
        disallow: ["/inboxes", "/api/", "/api-keys", "/domains", "/feedback"],
      },
    ],
    sitemap: `${SITE_URL}/sitemap.xml`,
    host: SITE_URL,
  };
}
