// The control server's types, as its JSON has them (proto/view.go,
// proto/ops.go, proto/web.go, proto/netmap.go). Field names follow the json
// tags; Go's time.Time is an RFC 3339 string, absent when zero.

export type PersonID = string;
export type LANID = string;
export type DeviceID = string;
export type InviteID = string;
export type RelayID = string;
export type ShareID = string;
export type ServiceKey = string; // "tcp/3389" or "tcp/8000-8100,udp/9000"

export type DeviceKind = 'laptop' | 'desktop' | 'mini' | 'server' | 'nas' | 'console' | 'phone' | 'tablet' | 'other';
export type OS = 'macos' | 'windows' | 'linux' | 'ios' | 'android' | 'other';
export type ServiceKind = 'screen' | 'files' | 'web' | 'shell' | 'game' | 'other';

export interface Service {
  kind: ServiceKind;
  name?: string;
  ports: string[]; // PortRange as text: "tcp/3389"
}

export interface ServiceState extends Service {
  on: boolean;
}

export interface Person {
  id: PersonID;
  name: string;
  displayName: string;
  initial: string;
  email?: string;
}

export type Role = 'owner' | 'member';
export type MemberState = 'joined' | 'invited' | 'pending';

export interface Member {
  person: PersonID;
  role: Role;
  state: MemberState;
  joinedAt: string;
}

export interface LANDevice {
  device: DeviceID;
  joinedAt: string;
  paused?: boolean;
  pending?: boolean;
  services: ServiceState[];
}

export type InviteState = 'pending' | 'accepted' | 'declined' | 'revoked' | 'expired';

export interface InviteView {
  id: InviteID;
  lan: LANID;
  inviter: PersonID;
  person?: PersonID;
  url: string;
  createdAt: string;
  expiresAt: string;
  state: InviteState;
}

export interface InvitedPerson {
  person: PersonID;
  invitedBy: PersonID;
  expiresAt: string;
  invite: InviteID;
}

export type LANMode = 'lan' | 'share';

export interface LANView {
  id: LANID;
  name: string;
  owner: PersonID;
  createdAt: string;
  members: Member[];
  devices: LANDevice[];
  relays: RelayID[];
  addedRelays?: RelayID[];
  invites?: InviteView[];
  invited?: InvitedPerson[];
  approveDevices?: boolean;
  approveMembers?: boolean;
  pending?: boolean;
  mode?: LANMode;
}

export interface LoginInfo {
  expiresAt?: string;
  renewFrom?: string;
  expired?: boolean;
}

export interface ShareView {
  id: ShareID;
  person: PersonID;
  sharer: PersonID;
  services: Service[];
  createdAt: string;
}

export interface DeviceView {
  id: DeviceID;
  owner: PersonID;
  name: string;
  displayName: string;
  fqdn: string;
  kind: DeviceKind;
  os: OS;
  addrs: string[];
  online: boolean;
  lastSeen?: string;
  lans: LANID[];
  services?: Service[];
  refused?: PersonID[];
  refusesMe?: boolean;
  shares?: ShareView[];
  publicLAN?: LANID;
  login?: LoginInfo;
}

export interface SharedDevice {
  share: ShareID;
  device: DeviceView;
  from: PersonID;
  fromLAN?: string;
  services: Service[];
  sharedAt: string;
}

export type RelaySource = 'server' | 'home' | 'public';

export interface Relay {
  id: RelayID;
  name: string;
  source: RelaySource;
  owner?: PersonID;
  hostName: string;
  ipv4?: string;
  ipv6?: string;
  derpPort: number;
  stunPort?: number;
  pin?: string;
  rateLimit?: number; // bytes a second per connection; 0 or absent: none
}

export interface RelayView {
  id: RelayID;
  lans: LANID[];
  registered: boolean;
  tokenExpiresAt?: string;
  online: boolean;
  lastSeen?: string;
  seeded?: boolean;
  since?: string;
  weight: number;
  capacity?: number;
  full?: boolean;
  // default: a public relay the deployment's seed lists; every LAN tries it
  // first, so it is in no share bar, and only kicks are the operators'.
  default?: boolean;
}

export interface RelayJoinView {
  id: string;
  relay?: RelayID;
  createdAt: string;
  expiresAt: string;
}

export interface RelayJoin {
  id: string;
  code: string;
  command: string;
  relay?: RelayID;
  expiresAt: string;
}

export interface Seats {
  used: number;
  limit: number;
  enforced: boolean;
  people: PersonID[];
  left: number;
}

export interface InvitePreview {
  invite: InviteID;
  lan: LANID;
  lanName: string;
  inviter: Person;
  state: InviteState;
  expiresAt: string;
  alreadyIn?: boolean;
  needsApproval?: boolean;
  seatsFull?: boolean;
  // Who is in it already and how many devices it has (no emails): given
  // while the link still works, or to someone in it already.
  members?: Person[];
  devices?: number;
}

export interface UserView {
  version: number;
  me: PersonID;
  people: Person[];
  lans: LANView[];
  devices: DeviceView[];
  relays: Relay[];
  myRelays?: RelayView[];
  relayJoins?: RelayJoinView[];
  seats: Seats;
  invitations?: InvitePreview[];
  features?: string[];
  sharedWithMe?: SharedDevice[];
  operator?: OperatorView;
  // M7: my devices that were logged out (and my LANs' public ones): in no
  // map, for 已登出 and a way to delete them (device.delete).
  loggedOutDevices?: DeviceView[];
}

export interface StreamFrame {
  version?: number;
  userView?: UserView;
  keepAlive?: boolean;
  loggedOut?: boolean;
}

export interface UsageLine {
  lan?: LANID;
  share?: ShareID;
  device?: DeviceID;
  bytes: number;
}

export interface RelayLoad {
  at: string;
  devices: number;
  people: number;
  connections: number;
  bytesPerSec: number;
  cpu: number; // 0–1
  memory: number;
  memoryLimit?: number;
  uptime: number; // seconds
  full?: boolean;
}

export interface RelayClient {
  device?: DeviceID;
  person: PersonID;
}

export interface RelayKick {
  id: string;
  person?: PersonID;
  device?: DeviceID;
  by?: PersonID;
  at: string;
  liftedAt?: string;
  liftedBy?: PersonID;
}

export interface RelayMonth {
  relay: RelayID;
  bytes: number;
  lans: UsageLine[];
  shares: UsageLine[];
  devices: UsageLine[];
  load?: RelayLoad;
  connected?: RelayClient[];
  kicks?: RelayKick[];
  // The operators' page: who used a public relay most this month (five at
  // most, most first), and how many used it at all.
  top?: PersonBytes[];
  users?: number;
}

export interface PersonBytes {
  person: Person; // no email
  bytes: number;
}

// The deployment's operators' page (M4-13): the public relays as their
// cards have them, and the join codes not used yet.
export interface OperatorView {
  relays: RelayView[];
  joins: RelayJoinView[];
}

// What the operators' page counts: people and devices on the public relays
// now (each once), and each public relay's month.
export interface OperatorUsage {
  month: string;
  people: number;
  devices: number;
  relays: RelayMonth[];
}

export interface RelayUsage {
  month: string;
  relays: RelayMonth[];
}

export type OpType =
  | 'lan.create' | 'lan.rename' | 'lan.delete' | 'lan.invite' | 'invite.accept' | 'invite.decline' | 'invite.revoke'
  | 'lan.addDevice' | 'lan.removeDevice' | 'service.set' | 'device.refuse' | 'device.rename' | 'device.setKind'
  | 'device.setDNSName' | 'device.renew' | 'member.remove' | 'lan.leave' | 'lan.setApproveDevices' | 'lan.setApproveMembers'
  | 'device.approve' | 'device.reject' | 'member.approve' | 'member.reject'
  | 'share.create' | 'share.revoke' | 'share.release' | 'share.leave' | 'lan.setMode' | 'device.setPublic'
  | 'relay.create' | 'relay.token' | 'relay.rename' | 'relay.delete' | 'relay.add' | 'relay.remove' | 'relay.join'
  | 'relay.revokeJoin' | 'relay.setWeight' | 'relay.setCapacity' | 'relay.kick' | 'relay.unkick'
  | 'device.delete' | 'account.delete';

export interface OpArgs {
  lan?: LANID;
  device?: DeviceID;
  person?: PersonID;
  name?: string;
  kind?: DeviceKind;
  invite?: string;
  service?: Service;
  on?: boolean;
  refuse?: boolean;
  invitation?: InviteID;
  share?: ShareID;
  services?: ServiceKey[];
  mode?: LANMode;
  relay?: RelayID;
  join?: string;
  weight?: number;
  capacity?: number;
  kick?: string;
  public?: boolean;
  // account.delete (M7): only say what would go; what becomes of my LANs
  // with others in them; my email again, to delete for real.
  dryRun?: boolean;
  ownedLANs?: 'transfer' | 'delete';
  confirm?: string;
}

export interface Op {
  id: string;
  op: OpType;
  args: OpArgs;
}

export interface OpOutput {
  lan?: LANID;
  invite?: InviteView;
  share?: ShareID;
  relay?: RelayID;
  relayToken?: string;
  relayJoin?: RelayJoin;
  kick?: string;
  account?: AccountDeletion; // account.delete (M7)
}

// AccountDeletion is what account.delete deletes, or would (dryRun):
// design/plan/m7/account-delete.md §2.
export interface AccountDeletion {
  dryRun?: boolean;
  devices: DeviceID[];
  lans: LANID[];
  transferred?: { lan: LANID; to: PersonID }[];
  left: LANID[];
  blocked?: LANID[];
  relays: RelayID[];
  relaysInUse?: RelayID[];
  shares: number;
  invites: number;
}

export interface OpResult {
  version: number;
  result?: OpOutput;
}

export type ErrorCode = 'unauthorized' | 'not_logged_in' | 'forbidden' | 'not_found' | 'invalid' | 'conflict' | 'seats_full' | 'internal';

export interface ErrorBody {
  error?: { code: ErrorCode; message: string };
}

export interface WebLoginRequest {
  email: string;
  code?: string;
}

export interface WebLoginResult {
  step: 'code' | 'done';
  account?: string;
  person?: PersonID;
}

// The Web API's paths (proto/web.go).
// The LANs the sidebar lists (DESIGN.md §6.11): a LAN in the device-sharing
// mode is its owner's; its members find what it gives them under 共享给我.
export const sharedOnly = (v: UserView, l: LANView): boolean => l.mode === 'share' && l.owner !== v.me;
export const listedLANs = (v: UserView): LANView[] => v.lans.filter((l) => !l.pending && !sharedOnly(v, l));

export const Paths = {
  login: '/api/v1/login',
  loginEmail: '/api/v1/login/email',
  loginCode: '/api/v1/login/code',
  invites: '/api/v1/invites/',
  logout: '/api/v1/logout',
  userView: '/api/v1/userview',
  stream: '/api/v1/stream',
  ops: '/api/v1/ops',
  relayUsage: '/api/v1/relay-usage',
  operatorUsage: '/api/v1/operator/relay-usage',
  invite: '/api/v1/invite',
} as const;

// The invite link's path on the server (proto/links.go InvitePath).
export const InvitePath = '/i/';

// fnv32a is FNV-1a over the string's UTF-8 bytes, as Go's hash/fnv New32a.
export function fnv32a(s: string): number {
  let h = 0x811c9dc5;
  for (const b of new TextEncoder().encode(s)) {
    h ^= b;
    h = Math.imul(h, 0x01000193) >>> 0;
  }
  return h >>> 0;
}

// colorIndex is a person's owner color on my screen, as the desktop picks
// it (client/model ColorFor): blue (0) for me, and for anyone else one of
// the other five by their id, the same in every LAN and on every visit.
export function colorIndex(me: PersonID, id: PersonID): number {
  if (id === me) return 0;
  return 1 + (fnv32a(id) % 5);
}
