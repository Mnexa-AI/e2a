import type { Meta, StoryObj } from "@storybook/react";
import { InkConsole } from "./InkConsole";

const meta = {
  title: "Surfaces/InkConsole",
  component: InkConsole,
  tags: ["autodocs"],
  parameters: { backgrounds: { default: "panel" } },
} satisfies Meta<typeof InkConsole>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Curl: Story = {
  args: {
    title: "send an email",
    lang: "bash",
    lines: [
      { c: "comment", text: "# POST a message as your agent" },
      { c: "prompt", text: "curl -X POST https://api.e2a.dev/v1/messages \\" },
      { c: "string", text: '  -H "Authorization: Bearer $E2A_KEY" \\' },
      { c: "string", text: '  -d \'{"to":"founder@acme.com","subject":"hi"}\'' },
    ],
  },
};

export const Output: Story = {
  args: {
    title: "response",
    lang: "json",
    copy: false,
    lines: [
      { c: "plain", text: "{" },
      { c: "accent", text: '  "id": "msg_abc123",' },
      { c: "string", text: '  "status": "queued"' },
      { c: "plain", text: "}" },
    ],
  },
};
