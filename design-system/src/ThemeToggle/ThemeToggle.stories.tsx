import type { Meta, StoryObj } from "@storybook/react";
import { useState } from "react";
import { ThemeToggle, type Theme } from "./ThemeToggle";

const meta = {
  title: "Controls/ThemeToggle",
  component: ThemeToggle,
  tags: ["autodocs"],
} satisfies Meta<typeof ThemeToggle>;

export default meta;
type Story = StoryObj<typeof meta>;

// Controlled component — drive it from local state in the story.
function Demo({ initial }: { initial: Theme }) {
  const [theme, setTheme] = useState<Theme>(initial);
  return <ThemeToggle value={theme} onChange={setTheme} />;
}

export const System: Story = {
  args: { value: "system", onChange: () => {} },
  render: () => <Demo initial="system" />,
};

export const Light: Story = {
  args: { value: "light", onChange: () => {} },
  render: () => <Demo initial="light" />,
};

export const Dark: Story = {
  args: { value: "dark", onChange: () => {} },
  render: () => <Demo initial="dark" />,
};
