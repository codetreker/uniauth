import type { Account, Session } from "./api";
export const fixtureUser: Account = {
  id: "usr_example",
  email: "member@example.test",
  name: "Example member",
  avatar_url: null,
  is_admin: false,
  created_at: "2026-10-12T09:00:00Z",
};
export const fixtureSession: Session = {
  csrf_token: "token",
  user: fixtureUser,
};
export const completeAuthorization = async (): Promise<Session> =>
  fixtureSession;
