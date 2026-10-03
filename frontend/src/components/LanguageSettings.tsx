import { ChevronDownIcon, LanguagesIcon } from "lucide-react";
import { Language, useI18n } from "../lib/i18n";

/** The interface language: a small picker beside the Settings title. */
export default function LanguageSettings() {
  const { language, setLanguage, t } = useI18n();
  return (
    <label className="lang-pick" title={t("Interface language")}>
      <LanguagesIcon size={15} />
      <select aria-label={t("Interface language")} value={language} onChange={(e) => setLanguage(e.target.value as Language)}>
        <option value="zh-CN">简体中文</option>
        <option value="en">English</option>
      </select>
      <ChevronDownIcon size={13} />
    </label>
  );
}
