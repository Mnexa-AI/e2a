import type { Meta, StoryObj } from "@storybook/react";
import { Collapsible } from "./Collapsible";

const meta = {
  title: "Surfaces/Collapsible",
  component: Collapsible,
  tags: ["autodocs"],
} satisfies Meta<typeof Collapsible>;

export default meta;
type Story = StoryObj<typeof meta>;

const body = (
  <div style={{ padding: "12px 18px", fontSize: 13, color: "var(--fg-muted)" }}>
    Received: by mx.e2a.dev with SMTP id abc123
    <br />
    SPF: pass · DKIM: pass · DMARC: pass
  </div>
);

export const Closed: Story = {
  args: { label: "Full headers", meta: "12 lines", children: body },
  render: (a) => (
    <div style={{ width: 420 }}>
      <Collapsible {...a} />
    </div>
  ),
};

export const Open: Story = {
  args: { label: "Full headers", meta: "12 lines", defaultOpen: true, children: body },
  render: (a) => (
    <div style={{ width: 420 }}>
      <Collapsible {...a} />
    </div>
  ),
};
