import type { Meta, StoryObj } from "@storybook/react";
import { Avatar } from "./Avatar";

const meta = {
  title: "Atoms/Avatar",
  component: Avatar,
  tags: ["autodocs"],
} satisfies Meta<typeof Avatar>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
  args: { name: "Ada Lovelace", email: "ada@acme.com", size: 32 },
};

export const FromEmail: Story = {
  args: { email: "founder@example.com", size: 32 },
};

export const Row: Story = {
  args: { email: "a@a.com" },
  render: () => (
    <div style={{ display: "flex", gap: 8 }}>
      <Avatar name="Ada Lovelace" email="ada@acme.com" size={28} />
      <Avatar name="Grace Hopper" email="grace@navy.mil" size={28} />
      <Avatar email="support@e2a.dev" size={28} />
      <Avatar name="Kit" email="kit@studio.io" size={28} />
    </div>
  ),
};
