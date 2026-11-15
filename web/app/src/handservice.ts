// A service set up by hand on My Devices (design/plan/m7/mydevices.md §6.1):
// a name and a port; the kind is guessed from the port, as the desktop's
// client/model.ServiceKindFor does, and TCP and UDP both open.
import type { Service, ServiceKind } from './model.ts';

export function serviceKindFor(port: number): ServiceKind {
  if ([3389, 5900].includes(port)) return 'screen';
  if ([445, 548, 139].includes(port)) return 'files';
  if (port === 22) return 'shell';
  if ([80, 443, 3000, 5000, 8000, 8080, 8443, 8888].includes(port)) return 'web';
  if ([25565, 27015, 7777, 19132].includes(port)) return 'game';
  return 'other';
}

const KIND_NAMES: Record<string, string> = { screen: '远程桌面', files: '共享文件夹', web: '网站', shell: '命令行', game: '游戏' };
export const kindName = (kind: string): string => KIND_NAMES[kind] || '服务';

export function handService(name: string, port: number): Service {
  const kind = serviceKindFor(port);
  return { kind, name: name.trim() || kindName(kind), ports: [`tcp/${port}`, `udp/${port}`] };
}

// portTaken is the service of services that already uses port, by TCP or
// UDP: two services on a device never share a port.
export function portTaken(services: Service[], port: number): Service | undefined {
  return services.find((s) =>
    s.ports.some((p) => {
      const [a, b] = (p.split('/')[1] || '').split('-').map(Number);
      return port >= a && port <= (b || a);
    }),
  );
}
