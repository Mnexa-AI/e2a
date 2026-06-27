import type { Meta, StoryObj } from "@storybook/react";
import { Logo } from "./Logo";

const meta = {
  title: "Brand/Logo",
  component: Logo,
  tags: ["autodocs"],
  argTypes: {
    variant: { control: "inline-radio", options: ["wordmark", "mark"] },
    tone: { control: "inline-radio", options: ["color", "mono", "ink"] },
    height: { control: { type: "number" } },
  },
} satisfies Meta<typeof Logo>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Wordmark: Story = {
  args: { variant: "wordmark", tone: "color", height: 48 },
};

export const Mark: Story = {
  args: { variant: "mark", tone: "color", height: 64 },
};

export const WordmarkInk: Story = {
  args: { variant: "wordmark", tone: "ink", height: 48 },
};

export const Mono: Story = {
  args: { variant: "wordmark", tone: "mono", height: 48 },
  // currentColor — show it inheriting an accent-colored context.
  decorators: [
    (Story) => (
      <span style={{ color: "var(--accent-strong)" }}>
        <Story />
      </span>
    ),
  ],
};
