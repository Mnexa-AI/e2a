import type { Meta, StoryObj } from "@storybook/react";
import { Card } from "./Card";
import { Eyebrow } from "../Eyebrow/Eyebrow";
import { Chip } from "../Chip/Chip";

const meta = {
  title: "Surfaces/Card",
  component: Card,
  tags: ["autodocs"],
} satisfies Meta<typeof Card>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
  render: () => (
    <Card style={{ width: 320 }}>
      <Eyebrow>Inbound mail</Eyebrow>
      <div style={{ marginTop: 8, display: "flex", alignItems: "center", gap: 8 }}>
        <Chip tone="success">delivered</Chip>
        <span style={{ color: "var(--fg-muted)", fontSize: 12 }}>2 min ago</span>
      </div>
    </Card>
  ),
};

export const Plain: Story = {
  render: () => (
    <Card style={{ width: 320 }}>
      <p style={{ margin: 0, color: "var(--fg)" }}>A simple surface container.</p>
    </Card>
  ),
};
