import { startStack } from "./harness";

/** Starts Postgres and glossa-server; the returned function tears them down. */
export default async function globalSetup(): Promise<() => Promise<void>> {
  return startStack();
}
