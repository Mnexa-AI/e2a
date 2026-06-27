import type { Meta, StoryObj } from "@storybook/react";
import { Dot } from "./Dot";

const meta = {
  title: "Atoms/Dot",
  component: Dot,
  tags: ["autodocs"],
  argTypes: {
    tone: {
      control: "inline-radio",
      options: ["success", "warn", "accent", "danger", "neutral"],
    },
  },
} satisfies Meta<typeof Dot>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Online: Story = { args: { tone: "success" } };
export const Degraded: Story = { args: { tone: "warn" } };
export const Down: Story = { args: { tone: "danger" } };
