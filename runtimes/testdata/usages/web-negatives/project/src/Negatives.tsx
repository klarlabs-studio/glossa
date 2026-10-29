import { T } from "@klarlabs-studio/glossa/react";

declare function Other(props: { id: string }): JSX.Element;

export function Negatives({ dynamicId, section }: { dynamicId: string; section: string }) {
  return (
    <section>
      <T id={dynamicId} />
      <T id={`nav.${section}`} />
      <p>Use t("fake.jsx-text") and {"t('fake.jsx-string')"} here.</p>
      {/* <T id="fake.jsx-comment" /> */}
      <Other id="fake.other-component" />
      <T id="nav.home" />
    </section>
  );
}
