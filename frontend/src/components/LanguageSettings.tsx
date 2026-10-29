import { LANGUAGES, Language, useI18n } from "../lib/i18n";
import { Row, Select } from "./settings/kit";

/** The interface language, as a row of the Appearance group in Settings. */
export default function LanguageSettings() {
  const { language, setLanguage } = useI18n();
  return (
    <Row label="Language">
      <Select label="Interface language" testId="language" value={language}
        options={LANGUAGES.map((l) => ({ value: l.id, label: l.name }))}
        onChange={(v) => setLanguage(v as Language)} />
    </Row>
  );
}
