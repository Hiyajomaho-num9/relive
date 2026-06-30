export function shouldLoadScanPathDerivedStatus(collapsed: boolean, loaded: boolean): boolean {
  return !collapsed && !loaded
}
