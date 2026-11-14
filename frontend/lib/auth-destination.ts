// Only invitations may survive an authentication round trip.
export function authDestination(next: string | null): string {
  return next === "/invitations" ? "/invitations" : "/overview";
}

export function authLink(path: string, next: string | null): string {
  return authDestination(next) === "/invitations"
    ? `${path}${path.includes("?") ? "&" : "?"}next=%2Finvitations`
    : path;
}
