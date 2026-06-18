import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

const mockList = vi.fn();
const mockCreate = vi.fn();
const mockVerify = vi.fn();
const mockDelete = vi.fn();

function pager(items: unknown[]) {
  return { toArray: vi.fn(async () => items) };
}

vi.mock("../sdk.js", () => ({
  createClient: vi.fn(() => ({
    domains: {
      list: mockList,
      create: mockCreate,
      verify: mockVerify,
      delete: mockDelete,
    },
  })),
}));

vi.mock("../config.js", () => ({
  loadConfig: vi.fn(() => ({
    api_key: "e2a_testkey",
    api_url: "https://e2a.dev",
    agent_email: "bot@agents.e2a.dev",
  })),
  requireApiKey: vi.fn(() => "e2a_testkey"),
}));

import {
  domainsList,
  domainsRegister,
  domainsVerify,
  domainsDelete,
} from "../commands/domains.js";

describe("domainsList", () => {
  let mockStdout: ReturnType<typeof vi.spyOn>;
  let mockStderr: ReturnType<typeof vi.spyOn>;

  beforeEach(() => {
    mockStdout = vi.spyOn(process.stdout, "write").mockImplementation(() => true);
    mockStderr = vi.spyOn(process.stderr, "write").mockImplementation(() => true);
    mockList.mockReset();
  });

  afterEach(() => {
    mockStdout.mockRestore();
    mockStderr.mockRestore();
    vi.clearAllMocks();
  });

  it("lists domains with verification status", async () => {
    mockList.mockReturnValue(
      pager([
        { domain: "mycompany.com", verified: true },
        { domain: "staging.dev", verified: false },
      ]),
    );

    await domainsList();

    expect(mockStdout).toHaveBeenCalledWith("mycompany.com  verified\n");
    expect(mockStdout).toHaveBeenCalledWith("staging.dev  unverified\n");
  });

  it("shows message when no domains", async () => {
    mockList.mockReturnValue(pager([]));

    await domainsList();

    expect(mockStderr).toHaveBeenCalledWith(
      "No domains registered. Run: e2a domains register <domain>\n",
    );
  });
});

describe("domainsRegister", () => {
  let mockStdout: ReturnType<typeof vi.spyOn>;
  let mockStderr: ReturnType<typeof vi.spyOn>;
  let mockExit: ReturnType<typeof vi.spyOn>;

  beforeEach(() => {
    mockStdout = vi.spyOn(process.stdout, "write").mockImplementation(() => true);
    mockStderr = vi.spyOn(process.stderr, "write").mockImplementation(() => true);
    mockExit = vi.spyOn(process, "exit").mockImplementation(() => {
      throw new Error("process.exit");
    });
    mockCreate.mockReset();
  });

  afterEach(() => {
    mockStdout.mockRestore();
    mockStderr.mockRestore();
    mockExit.mockRestore();
    vi.clearAllMocks();
  });

  it("exits when no domain provided", async () => {
    await expect(domainsRegister(undefined)).rejects.toThrow("process.exit");
    expect(mockStderr).toHaveBeenCalledWith(expect.stringContaining("Usage:"));
  });

  it("registers domain and shows DNS records", async () => {
    mockCreate.mockResolvedValue({
      domain: "mycompany.com",
      dnsRecords: {
        mx: { host: "mycompany.com", value: "mx.e2a.dev", priority: 10 },
        txt: { host: "mycompany.com", value: "e2a-verify=abc123" },
      },
    });

    await domainsRegister("mycompany.com");

    expect(mockCreate).toHaveBeenCalledWith({ domain: "mycompany.com" });
    expect(mockStdout).toHaveBeenCalledWith("Registered: mycompany.com\n");
    expect(mockStdout).toHaveBeenCalledWith(
      expect.stringContaining("MX"),
    );
    expect(mockStdout).toHaveBeenCalledWith(
      expect.stringContaining("TXT"),
    );
  });
});

describe("domainsVerify", () => {
  let mockStdout: ReturnType<typeof vi.spyOn>;
  let mockStderr: ReturnType<typeof vi.spyOn>;
  let mockExit: ReturnType<typeof vi.spyOn>;

  beforeEach(() => {
    mockStdout = vi.spyOn(process.stdout, "write").mockImplementation(() => true);
    mockStderr = vi.spyOn(process.stderr, "write").mockImplementation(() => true);
    mockExit = vi.spyOn(process, "exit").mockImplementation(() => {
      throw new Error("process.exit");
    });
    mockVerify.mockReset();
  });

  afterEach(() => {
    mockStdout.mockRestore();
    mockStderr.mockRestore();
    mockExit.mockRestore();
    vi.clearAllMocks();
  });

  it("exits when no domain provided", async () => {
    await expect(domainsVerify(undefined)).rejects.toThrow("process.exit");
  });

  it("shows verified status", async () => {
    mockVerify.mockResolvedValue({ domain: "mycompany.com", verified: true });

    await domainsVerify("mycompany.com");

    expect(mockStdout).toHaveBeenCalledWith("Verified: mycompany.com\n");
  });

  it("shows not-yet-verified status", async () => {
    mockVerify.mockResolvedValue({ domain: "mycompany.com", verified: false });

    await domainsVerify("mycompany.com");

    expect(mockStderr).toHaveBeenCalledWith("Not yet verified: mycompany.com\n");
  });
});

describe("domainsDelete", () => {
  let mockStdout: ReturnType<typeof vi.spyOn>;
  let mockStderr: ReturnType<typeof vi.spyOn>;
  let mockExit: ReturnType<typeof vi.spyOn>;

  beforeEach(() => {
    mockStdout = vi.spyOn(process.stdout, "write").mockImplementation(() => true);
    mockStderr = vi.spyOn(process.stderr, "write").mockImplementation(() => true);
    mockExit = vi.spyOn(process, "exit").mockImplementation(() => {
      throw new Error("process.exit");
    });
    mockDelete.mockReset();
  });

  afterEach(() => {
    mockStdout.mockRestore();
    mockStderr.mockRestore();
    mockExit.mockRestore();
    vi.clearAllMocks();
  });

  it("exits when no domain provided", async () => {
    await expect(domainsDelete(undefined)).rejects.toThrow("process.exit");
  });

  it("deletes domain and confirms", async () => {
    mockDelete.mockResolvedValue(undefined);

    await domainsDelete("mycompany.com");

    expect(mockDelete).toHaveBeenCalledWith("mycompany.com");
    expect(mockStdout).toHaveBeenCalledWith("Deleted: mycompany.com\n");
  });
});
