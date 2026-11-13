// Shared state of the account switcher: whether the administrator's "Sign in
// as a user" picker is open (the account menu, the command palette and the
// administration pages all open the same one).
export const picker = $state({ open: false });
