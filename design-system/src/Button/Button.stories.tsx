import type { Meta, StoryObj } from "@storybook/react";
import { Button } from "./Button";

const meta = {
  title: "Atoms/Button",
  component: Button,
  tags: ["autodocs"],
  argTypes: {
    variant: { control: "inline-radio", options: ["primary", "ghost", "mono"] },
  },
} satisfies Meta<typeof Button>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Primary: Story = {
  args: { variant: "primary", children: "Send email" },
};

export const Ghost: Story = {
  args: { variant: "ghost", children: "Cancel" },
};

export const Mono: Story = {
  args: { variant: "mono", children: "agent.run()" },
};

export const Disabled: Story = {
  args: { variant: "primary", children: "Send email", disabled: true },
};
