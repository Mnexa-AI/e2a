import type { Meta, StoryObj } from "@storybook/react";
import { useState } from "react";
import { Field } from "./Field";

const meta = {
  title: "Forms/Field",
  component: Field,
  tags: ["autodocs"],
} satisfies Meta<typeof Field>;

export default meta;
type Story = StoryObj<typeof meta>;

function Demo(args: { label: string; hint?: string; placeholder?: string; initial?: string }) {
  const [v, setV] = useState(args.initial ?? "");
  return (
    <div style={{ width: 320 }}>
      <Field label={args.label} hint={args.hint} placeholder={args.placeholder} value={v} onChange={setV} />
    </div>
  );
}

export const Default: Story = {
  args: { label: "Agent name", value: "", onChange: () => {} },
  render: () => <Demo label="Agent name" placeholder="support-bot" />,
};

export const WithHint: Story = {
  args: { label: "Forward address", value: "", onChange: () => {} },
  render: () => (
    <Demo
      label="Forward address"
      placeholder="agent@acme.com"
      hint="Inbound mail is signed and delivered to this endpoint."
    />
  ),
};

export const Filled: Story = {
  args: { label: "Reply-to", value: "", onChange: () => {} },
  render: () => <Demo label="Reply-to" initial="founder@acme.com" />,
};
