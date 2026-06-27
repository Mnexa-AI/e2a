import type { Meta, StoryObj } from "@storybook/react";
import { Chip } from "./Chip";

const meta = {
  title: "Atoms/Chip",
  component: Chip,
  tags: ["autodocs"],
  argTypes: {
    tone: {
      control: "inline-radio",
      options: ["success", "warn", "info", "accent", "danger", "neutral"],
    },
    mono: { control: "boolean" },
  },
} satisfies Meta<typeof Chip>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Delivered: Story = { args: { tone: "success", children: "delivered" } };
export const Pending: Story = { args: { tone: "warn", children: "pending review" } };
export const Bounced: Story = { args: { tone: "danger", children: "bounced" } };
export const MessageId: Story = {
  args: { tone: "neutral", mono: true, children: "msg_abc123" },
};
