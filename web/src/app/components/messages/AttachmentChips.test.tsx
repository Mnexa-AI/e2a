import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import {
  AttachmentChips,
  downloadableAttachments,
} from "./AttachmentChips";
import type { AttachmentMeta } from "../types";

jest.mock("../onboarding/api", () => ({
  getAttachment: jest.fn(),
  loadAttachmentObjectUrl: jest.fn(),
}));
import { getAttachment, loadAttachmentObjectUrl } from "../onboarding/api";

const inlineImg: AttachmentMeta = {
  index: 0,
  filename: "image.png",
  content_type: "image/png",
  size_bytes: 1000,
  content_id: "ii_a@mail",
};
const pdf: AttachmentMeta = {
  index: 1,
  filename: "report.pdf",
  content_type: "application/pdf",
  size_bytes: 200000,
};
const archive: AttachmentMeta = {
  index: 2,
  filename: "logs.zip",
  content_type: "application/zip",
  size_bytes: 4096,
};

describe("downloadableAttachments", () => {
  it("excludes inline images referenced by a cid: in the body", () => {
    const html = '<p>hi</p><img src="cid:ii_a@mail">';
    expect(downloadableAttachments([inlineImg, pdf], html)).toEqual([pdf]);
  });

  it("includes an image attachment NOT referenced inline", () => {
    // Same image, but the body never references its content_id → it's a real
    // (downloadable) attachment, not an inline image.
    expect(downloadableAttachments([inlineImg, pdf], "<p>no images</p>")).toEqual([
      inlineImg,
      pdf,
    ]);
  });

  it("treats every attachment as downloadable when there is no HTML body", () => {
    expect(downloadableAttachments([inlineImg, pdf], undefined)).toEqual([
      inlineImg,
      pdf,
    ]);
  });

  it("returns [] for no attachments", () => {
    expect(downloadableAttachments([], "<p>x</p>")).toEqual([]);
    expect(downloadableAttachments(undefined, "<p>x</p>")).toEqual([]);
  });
});

describe("AttachmentChips", () => {
  beforeEach(() => jest.clearAllMocks());

  it("renders a chip per attachment with filename and size", () => {
    render(
      <AttachmentChips email="a@x.dev" messageId="msg_1" attachments={[pdf]} />,
    );
    const chip = screen.getByTestId("attachment-chip");
    expect(chip).toHaveTextContent("report.pdf");
    expect(chip).toHaveTextContent("195 KB");
  });

  // A renderable attachment previews in-app; downloading is then an explicit
  // choice inside the viewer rather than the only way to look at the file.
  it("opens the in-app viewer for a previewable attachment", async () => {
    (loadAttachmentObjectUrl as jest.Mock).mockResolvedValue({
      url: "blob:http://localhost/abc",
      revoke: jest.fn(),
    });
    render(
      <AttachmentChips email="a@x.dev" messageId="msg_1" attachments={[pdf]} />,
    );
    fireEvent.click(screen.getByTestId("attachment-chip"));
    await waitFor(() =>
      expect(screen.getByTestId("attachment-viewer")).toBeInTheDocument(),
    );
    // Previewing must not fall through to the download path.
    expect(getAttachment).not.toHaveBeenCalled();
  });

  // Nothing to render → clicking still downloads, as it always did. Pins that
  // the viewer didn't swallow the download path for unrenderable types.
  it("downloads directly for an attachment it cannot preview", async () => {
    (getAttachment as jest.Mock).mockResolvedValue({
      download_url: "https://api.e2a.dev/v1/…/download?token=t",
    });
    // jsdom: <a>.click() would navigate; stub it so the click is observable.
    const clickSpy = jest
      .spyOn(HTMLAnchorElement.prototype, "click")
      .mockImplementation(() => {});
    render(
      <AttachmentChips email="a@x.dev" messageId="msg_1" attachments={[archive]} />,
    );
    fireEvent.click(screen.getByTestId("attachment-chip"));
    await waitFor(() =>
      expect(getAttachment).toHaveBeenCalledWith("a@x.dev", "msg_1", 2),
    );
    expect(clickSpy).toHaveBeenCalled();
    expect(screen.queryByTestId("attachment-viewer")).not.toBeInTheDocument();
    clickSpy.mockRestore();
  });
});
