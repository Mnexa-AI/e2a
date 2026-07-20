// previewKind decides whether an attachment gets an in-app render or a plain
// download. Getting it wrong in either direction is user-visible: a false
// positive renders a broken embed, a false negative hides a viewable file
// behind a download.

import { previewKind, canPreview } from "./attachmentPreview";

describe("previewKind", () => {
  it("recognises images and PDFs", () => {
    expect(previewKind("image/jpeg")).toBe("image");
    expect(previewKind("image/png")).toBe("image");
    expect(previewKind("image/svg+xml")).toBe("image");
    expect(previewKind("application/pdf")).toBe("pdf");
  });

  // content_type comes straight off the MIME part, so it arrives with
  // parameters and inconsistent casing far more often than not.
  it("tolerates parameters and casing from the wire", () => {
    expect(previewKind("IMAGE/JPEG")).toBe("image");
    expect(previewKind("image/jpeg; name=photo.jpg")).toBe("image");
    expect(previewKind("Application/PDF; charset=binary")).toBe("pdf");
    expect(previewKind("  image/png  ")).toBe("image");
  });

  it("returns null for types the browser can't render natively", () => {
    expect(previewKind("application/zip")).toBeNull();
    expect(previewKind("text/csv")).toBeNull();
    expect(previewKind("application/octet-stream")).toBeNull();
    // Not "application/pdf" — a prefix match here would embed the wrong thing.
    expect(previewKind("application/pdfx")).toBeNull();
  });

  it("returns null when the type is missing entirely", () => {
    expect(previewKind(undefined)).toBeNull();
    expect(previewKind("")).toBeNull();
  });

  it("canPreview mirrors previewKind", () => {
    expect(canPreview("image/gif")).toBe(true);
    expect(canPreview("application/pdf")).toBe(true);
    expect(canPreview("application/zip")).toBe(false);
    expect(canPreview(undefined)).toBe(false);
  });
});
