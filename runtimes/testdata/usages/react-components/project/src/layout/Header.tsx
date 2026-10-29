import { Component, memo } from "react";
import { T, useGlossa } from "@klarlabs-studio/glossa/react";

function pageTitle(t: (id: string) => string): string {
  return t("layout.title");
}

export const Header = () => {
  const { t } = useGlossa();
  return (
    <header>
      <h1>{pageTitle(t)}</h1>
      <T id="nav.home" />
    </header>
  );
};

export const Card = memo(function Card() {
  return <T id="layout.card" />;
});

export class LegacyBanner extends Component {
  render() {
    return <T id="legacy.banner">Alte Version</T>;
  }
}

export default function () {
  return <T id="layout.fallback" />;
}
