import { useThemeContext, type ThemeMode } from "@/hooks/theme";

const themes: Array<{ value: ThemeMode; label: string }> = [
  { value: "light", label: "浅色" },
  { value: "dark", label: "深色" },
];

export function ThemeToggle() {
  const { theme, setTheme } = useThemeContext();
  return (
    <div className="theme-toggle" role="group" aria-label="主题切换">
      {themes.map((item) => (
        <button
          key={item.value}
          type="button"
          className={theme === item.value ? "active" : undefined}
          onClick={() => setTheme(item.value)}
        >
          {item.label}
        </button>
      ))}
    </div>
  );
}
