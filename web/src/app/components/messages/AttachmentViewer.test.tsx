// In-app attachment viewer: renders what the browser can render natively,
// says so plainly when it can't, and never leaks the object URL it created.

import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { AttachmentViewer } from "./AttachmentViewer";
import type { AttachmentMeta } from "../types";

jest.mock("../onboarding/api", () => ({
  loadAttachmentObjectUrl: jest.fn(),
}));
import { loadAttachmentObjectUrl } from "../onboarding/api";

const image: AttachmentMeta = {
  index: 0,
  filename: "chart.jpg",
  content_type: "image/jpeg",
  size_bytes: 433332,
};
const pdf: AttachmentMeta = {
  index: 1,
  filename: "invoice.pdf",
  content_type: "application/pdf",
  size_bytes: 1067,
};
const archive: AttachmentMeta = {
  index: 2,
  filename: "logs.zip",
  content_type: "application/zip",
  size_bytes: 4096,
};

const BLOB = "blob:http://localhost/abc";
let revoke: jest.Mock;

beforeEach(() => {
  jest.clearAllMocks();
  revoke = jest.fn();
  (loadAttachmentObjectUrl as jest.Mock).mockResolvedValue({ url: BLOB, revoke });
});

function show(att: AttachmentMeta, onClose = jest.fn()) {
  return {
    onClose,
    ...render(
      <AttachmentViewer
        email="support@acme.dev"
        messageId="msg_1"
        att={att}
        onClose={onClose}
      />,
    ),
  };
}

describe("AttachmentViewer", () => {
  it("renders an image from the fetched object URL", async () => {
    show(image);
    const img = await screen.findByTestId("attachment-viewer-image");
    expect(img).toHaveAttribute("src", BLOB);
    expect(img).toHaveAttribute("alt", "chart.jpg");
  });

  // The PDF path is the reason bytes load as a blob: URL rather than the
  // data: URL the email-body images use — Chrome won't render a PDF from
  // data:. Pins that PDFs go to an iframe carrying that URL.
  it("renders a PDF in an iframe from the object URL", async () => {
    show(pdf);
    const frame = await screen.findByTestId("attachment-viewer-pdf");
    expect(frame).toHaveAttribute("src", BLOB);
    expect(screen.queryByTestId("attachment-viewer-image")).not.toBeInTheDocument();
  });

  // An honest "can't show this" beats an embed that renders as garbage.
  it("offers download only, with no embed, for an unrenderable type", async () => {
    show(archive);
    await waitFor(() =>
      expect(screen.getByTestId("attachment-viewer-download")).toBeInTheDocument(),
    );
    expect(screen.getByText(/No in-app preview/i)).toBeInTheDocument();
    expect(screen.queryByTestId("attachment-viewer-image")).not.toBeInTheDocument();
    expect(screen.queryByTestId("attachment-viewer-pdf")).not.toBeInTheDocument();
  });

  // Download reuses the already-fetched bytes rather than costing a second
  // round trip, and carries the real filename rather than the blob's uuid.
  it("downloads from the same object URL under the attachment filename", async () => {
    show(pdf);
    const link = await screen.findByTestId("attachment-viewer-download");
    expect(link).toHaveAttribute("href", BLOB);
    expect(link).toHaveAttribute("download", "invoice.pdf");
  });

  it("shows an error instead of an empty frame when the bytes fail to load", async () => {
    (loadAttachmentObjectUrl as jest.Mock).mockRejectedValue(new Error("boom"));
    show(pdf);
    expect(await screen.findByText(/Couldn't load this attachment/i)).toBeInTheDocument();
    expect(screen.queryByTestId("attachment-viewer-pdf")).not.toBeInTheDocument();
  });

  // An object URL pins its bytes in memory until revoked — a 400 KB image per
  // open would accumulate for the life of the tab.
  it("revokes the object URL on unmount", async () => {
    const { unmount } = show(image);
    await screen.findByTestId("attachment-viewer-image");
    expect(revoke).not.toHaveBeenCalled();
    unmount();
    expect(revoke).toHaveBeenCalled();
  });

  it("closes on Escape, on the backdrop, and on the close button", async () => {
    const onCloseEsc = jest.fn();
    const { unmount } = show(image, onCloseEsc);
    await screen.findByTestId("attachment-viewer-image");
    fireEvent.keyDown(document, { key: "Escape" });
    expect(onCloseEsc).toHaveBeenCalled();
    unmount();

    const onCloseBackdrop = jest.fn();
    const { unmount: u2 } = show(image, onCloseBackdrop);
    await screen.findByTestId("attachment-viewer-image");
    fireEvent.click(screen.getByTestId("attachment-viewer"));
    expect(onCloseBackdrop).toHaveBeenCalled();
    u2();

    const onCloseButton = jest.fn();
    show(image, onCloseButton);
    await screen.findByTestId("attachment-viewer-image");
    fireEvent.click(screen.getByLabelText(/close attachment preview/i));
    expect(onCloseButton).toHaveBeenCalled();
  });

  // Clicking the image itself must not fall through to the backdrop handler,
  // or the viewer would close the moment you tried to interact with it.
  it("does not close when the preview itself is clicked", async () => {
    const { onClose } = show(image);
    const img = await screen.findByTestId("attachment-viewer-image");
    fireEvent.click(img);
    expect(onClose).not.toHaveBeenCalled();
  });
});
