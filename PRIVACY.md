# Deneb Privacy Notice

> This notice explains in plain words what Deneb records about you, who can see it, how long it is kept and how you can delete it. Each point is written against the code in this repository. Deneb is open source, so you can check any of it yourself.

## The short version

- **Deneb cannot see what your devices send each other.**
  - Traffic between devices is end-to-end encrypted with WireGuard. When two devices can connect directly, they do, and the traffic does not pass through Deneb's servers.
  - When they cannot, a relay passes the traffic along. A relay only forwards encrypted packets and cannot decrypt them.
- **The control server keeps what it needs so that your devices can find each other and you can manage them**: your email address, what your devices are called and at which addresses they can be reached, which LANs you have set up, and what you have opened to whom.
- **The people who run the server can see these records**, but not what you send, and they cannot get the private keys on your devices.

## 1. Who runs Deneb and where the data is

- The control server and the public relays that Deneb connects to by default are run by the project's author, on cloud servers in **Hong Kong**. "The operator" below means the project's author.
- You can also run Deneb yourself. Then all the data stays on your own machines, and you are the operator.
- Two other services handle a little of your information:
  - Resend, the email service that sends sign-in codes: it sees your email address, the code and the sign-in link.
  - A group-chat bot on which the operator will receive alerts. None is chosen yet, and for now alerts are only written to the server's log. Alerts contain no personal information (section 2).

## 2. What the control server records

### Your account

- Your email address;
- Your name: the name others see, the letter in your avatar, and a short name that goes into your devices' network names;
- When the account was created;
- Settings the operator gave you: whether you are an admin of the deployment, and whether your seat limit was changed for you.

### Your devices

- The name you gave it, the computer's name in the system (host name), what kind of device it is, its operating system, the version of its Deneb engine, and which newer features the engine supports;
- The picture you set for it, if you set one. The picture you pick is first cropped on your computer to a 128 × 128 PNG with its photo metadata removed, and then uploaded. Everyone who can see the device (people in the same LAN, people you shared it with) can see the picture. It is deleted when you go back to the device icon or delete the device;
- The device's three **public keys**. The private keys stay on the device; the server does not have them;
- The addresses Deneb gave it: one IPv4 and one IPv6;
- **The addresses and ports it reports**: its public address, and its address on your local network (such as the one your home router gave it). Devices use these to connect to each other directly through routers ("hole punching"). They are cleared when the device signs out or its sign-in expires (section 7);
- Which relays it is connected to, which relays it tried and could not reach (and why: no connection, timed out, wrong certificate, refused), and which LANs you paused on this device. When a device cannot reach a relay, the relay's owner sees on the relay's card which of their own devices cannot reach it (only their own devices);
- When it signed in, when the sign-in expires, when it was last online, and whether it has signed out.

### Device monitoring

- Every 15 seconds the engine reads the processor model, the number of logical cores, CPU usage, the operating system and architecture, total and available physical memory, total and available space on local fixed disks, uptime and the engine version, and reports them over its authenticated connection.
- It does not read the list of processes, the contents of files, disk serial numbers or network drives. For disks, only the name of each volume or mount point and its space are reported, nothing about the files in your folders.
- The control server keeps only the latest report of each device, in memory, for at most 24 hours. A restart clears it, and it is never written to the database, the logs or the backups. Each time the data is read, the server checks again that the device belongs to the person asking; people in the same LAN and people a device is shared with cannot get it. The operator can technically read the server's memory.
- The app fetches the data every 15 seconds, and only while the My devices page is visible; it stops when you leave the page or hide the window. When a device is offline or its report is more than 45 seconds old, the app marks the figures as the last sample. Figures that were not collected stay blank, which does not mean 0.
- Latency is the round-trip time your own engine measures over the actual path to that device, not a figure the device reports. When there is no shared network, or nothing has been measured yet, the app says so.

### The networks you set up

- **LANs**: the name, who created it, its members and when they joined, its devices, and the two switches for approving new devices and new members.
- **Invitations**: who sent it, whom it was addressed to, whether it has been used and whether it has expired. The invitation link itself is kept too, because members need to be able to see it again.
- **Shares**: which device was shared with whom, which services are open to them, when it was shared, and also when it was taken back or given up.
- **Services**: the services you set up on a device (such as Remote Desktop or a shared folder: a name and a port), and which LANs and shares each one is open to.
- **Refusals**: whom you turned away, and on which device.
- **Your own relays ("My servers")**: the name, address, port, certificate fingerprint, speed limit, allocation share and the most devices at once; and who was kicked off, by whom, when, and when that was undone.
  - When it is installed, a relay also reports some facts about its machine: the program's version; how it was installed (system service, user service, Docker, and so on); the addresses on its network interfaces, local ones included; the result of the router's automatic port mapping (the public address the router reports); which system firewall it found and whether the ports are open; and how many times it started in the last hour. These are used to tell whether it can be reached from the internet, and if it cannot, why and what to do. Only you and the server can see them.
  - The results of checking it from the internet (from the control server and from another of the operator's machines, whether each could reach it) are kept in the server's memory, not in the database. When it last became usable is recorded in the database.
  - After you delete it in Deneb, uninstall it from its server or install it again somewhere else, the old copy's key (only its hash is stored) is kept for 30 more days, so that the old copy learns it should stop.
- **Relay traffic**: how many bytes each device sent and received through each relay each day. Only the amount, not whom the device talked to.

### Signing in

- **Sign-in codes**:
  - For a normal account, each sign-in sends a new 6-digit code. The code is kept only in the server's memory, is valid for 10 minutes and works once.
  - When you sign in on the web, the email also has a sign-in link, valid for 10 minutes like the code. Once you use one of them, the other stops working. The server keeps only a hash of the link, in memory, and does not write the link to the log.
  - Test accounts (addresses on domains meant for testing, such as `.test`) have fixed codes, written in the server's settings file, and are sent no email.
- **Devices signing in**: the computer's name, its operating system and the address the sign-in came from, kept in memory for at most an hour.
- **Web sign-ins**: only a hash of the session token (the token cannot be recovered from the hash, so the hash cannot be used to pose as you), when the sign-in started and when it expires (30 days), and the address you signed in from.
- **Device tokens**: not stored. The server signs them and gives them to the device, and checks the signature when they are used.

### Logs

The server's log is for finding problems. It is written to the server's own files, one a day, kept 30 days and readable only by the server. It contains:

- The email address and the source address when someone signs in or asks for a code. An email address without an account is not written as it is, only its domain and a short hash, such as `"…@example.com" #3fa2c1d07b9e`:
  - This still shows when someone is trying addresses at random: the same address asking for codes again and again within a short time, or one source address trying many addresses.
  - But it does not show which address it was. The key for the hash is made at random each time the server starts and is kept only in memory, so after a restart the same address gets a different hash.
- Devices coming online and going offline, with the device's name, its owner's email address and its engine version;
- The kind of each action and which device made it, such as "created a LAN", but not what the action contained;
- The source address of a device that a relay turned away;
- How many items the daily cleanup (section 7) deleted; only the counts;
- When an account is deleted, the account's number, with the email address masked like one without an account;
- Alerts (see "Monitoring" below).

Not in the log: codes, tokens, keys, the token in an invitation link, or anything you send. Of a sign-in link, only the first 4 characters are written.

Admins of the deployment can read today's and yesterday's log in the web admin console, so they see the email addresses and source addresses of sign-ins. Before the log is shown to them, the addresses and ports that devices report, tokens and codes are masked. Older logs can only be read with commands on the server.

The server container's own log (kept by Docker) only has the few lines from before the server starts writing its files, and what a crash prints. It rolls over by size, 30 MB at most. Each upgrade saves a copy of it, and saved copies are also kept only 30 days.

### Admin records

- Every change an admin makes is recorded: when, who, to whom, what, and the result. For example, "kittors deleted the account of Leo (leo@example.com) for him", or "kittors deleted Jay's “Old NAS”".
- The changes recorded are: deleting someone's account for them, deleting devices, making a device sign in again, signing someone out in their browsers, changing seat limits, making and removing admins, approving sign-ins of test devices, and, on the public relays, kicking devices off, changing allocation shares and adding a relay. Changes made in the web admin console, on the public relays page and with the commands on the server are all recorded.
- Only the changes are recorded, not whose cards an admin opened or what they looked at.
- Records are kept 1 year, then deleted. After an account is deleted, the records still show its name and email address until each record expires.
- A line is also written to the server's log.

### Backups

- One database backup a day: one for each of the last 7 days, and before that one a week, kept 8 weeks. They are on the same server and readable only by the server. The signing keys are not in the backups.
- Before each upgrade of the server, the whole data folder is also copied (without the logs and the daily backups). The last 10 copies are kept, for two months at most.

### Monitoring

- Every minute the server checks itself: whether the public relays are online and whether they are full, whether the backups are recent, whether there is enough disk space and memory, and whether many devices went offline at once.
- A watchdog on another machine checks the server and the relays every minute.
- The control server connects to each of your own relays from the internet: after it is installed, when it asks, when its address changes, and every 10 minutes. Over TCP it makes one TLS handshake and checks the certificate; over UDP it sends one STUN request. Nothing is passed through the relay. The watchdog on the other machine does the same, and all it gets is the relay's address, port and certificate fingerprint. Only public addresses are contacted, never private ones.
- When something is wrong, an alert is raised. For now it is only written to the log; later it will go to a group-chat bot the operator picks. An alert only has things like a relay's name, a number of devices or a backup's file name, and no names of people, email addresses or IP addresses. The last 200 alerts are kept in the server's memory.

## 3. What other people can see about you

- **People in the same LAN as you**:
  - They see you: your display name, the letter in your avatar and your short name.
  - They see your devices: name, kind, operating system, Deneb address, whether it is online, when it was last online, and the services you opened to this LAN.
  - Their devices get the public address, local address and ports your devices report, so that they can connect to yours directly.
  - **They do not see your email address.** Only you see it.
- **People you shared a device with**: they see that device and the services you opened to them, not your other devices.
- **Anyone with an invitation link**: when they open it, they see the LAN's name, who invited them, the names of the people already in it and how many devices it has. They do not see email addresses.
- **The owner of a relay under "My servers"**: sees which devices and people connect through it and how much traffic each one used, and which of the owner's own devices cannot reach it. Not the content.
- **Admins of the deployment** (on the operations page): see the names and traffic of the 5 people who used the public relays most this month, but not whom those people connected with.

## 4. What a relay can see

- The packets it forwards are encrypted. It cannot decrypt them.
- It sees:
  - the address and port that connected to it;
  - the device's public key;
  - when the device connected, and how many bytes it forwarded.

  When something goes wrong, its log has the other side's address and a short part of its public key. The log rolls over by size, 30 MB at most (10 MB a file, 3 files). A relay under "My servers" only has this limit if it was installed with the current one-line install command; one installed earlier has to be installed again.
- Every minute it reports to the control server how many bytes each device sent and received, and the server adds them up by day (section 2).
- Devices ask a relay "what is my public address?" (STUN), so the relay sees your public address.
- The public relays are in Hong Kong and run by the operator. A relay under "My servers" is run by its owner, on the owner's own machine.

## 5. What stays on your device

- **The engine's folder**:
  - Where it is:
    - macOS: `~/Library/Application Support/Deneb/engine`; for the system service, `/Library/Application Support/Deneb/engine`;
    - Windows: `%LOCALAPPDATA%\Deneb`; for the system service, `%ProgramData%\Deneb`;
    - Linux: `~/.local/share/deneb`; for the system service, `/var/lib/deneb`.

    Only your own user account (for a system service, only the system) and the computer's administrators can read it.
  - `state.json`: this device's private keys, which server it connects to, whether it is signed in, the numbers of the device and the account, and which LANs are paused.
  - `netmap.json`: the latest network map, which has the names and addresses of other people's devices in the same LANs.
  - `denebd.log`: the engine's log, with addresses, ports, connections and the problems it ran into. It does not record what you send.
    - Windows system service: at each start, a log over 10 MB is replaced by a new one, and one old file is kept.
    - macOS system service: written to the folder above. It is not rolled over by size.
    - Linux system service: written to the system log (journald), kept as long as the system's settings say.
    - An engine started by the Deneb window: a log over 10 MB is replaced by a new one, and one old file is kept, both at start and while it runs.
- **The Deneb window itself** only keeps where devices sit on the canvas, and small marks such as "the first-run guide is done".
- **Diagnostics bundles**:
  - One is made only when you export it in the app, or run `deneb diag` on the command line. The app saves it to your Downloads folder; the command saves it to the current folder, or where `-o` says.
  - It is **never sent anywhere by itself**. Whether to send it, and to whom, is up to you.
  - It contains: the engine's version and settings; the state shown in the window (devices, LANs, problems); the path to each device and how fast it is; the relay connections; the latest network map; in kernel mode, the network interface and routes Deneb set up in the system; the end of the log; and what the engine is busy with.
  - It does not contain: any private key (public keys are cut to their last 4 characters), sign-in or invitation links, the people and invitations in your account, or anything you send.
  - Think before you send it to someone: the network map has the names and addresses of other people's devices in the same LANs.

## 6. Other places Deneb connects to

- **Checking for updates**:
  - Release builds of the app check for updates in the background once a day, on the operator's download site, which is on the same server as the web app (below). They fetch this platform's update list and, when there is a new version, its package. The requests carry no account or device information, but the site sees your IP address. It keeps no access log.
  - Development builds never check, and neither does the Linux `.deb` package, which is updated by a newer `.deb`.
  - Your choices about updates (whether to check automatically, skipped versions, when it last checked) are kept on this computer in `updater.json`.
  - A relay under "My servers" that was installed from a release checks the same site for a newer relay 10 minutes after it starts and then about every 6 hours, and updates itself. Its requests carry only the relay's version, and the site sees the IP address of the relay's machine.
- **Sign-in emails**: sent through Resend, the email service the operator uses. It sees your email address, the code and the sign-in link, and handles them under its own privacy policy. The small star icon in the email is attached to the message, so opening the email does not fetch images from other sites. Test accounts are sent no email.
- **Alerts**: later they will go to a group-chat bot the operator picks (for now they are only written to the log). They contain no personal information.
- **The web app**:
  - It uses a subdomain of the operator's domain, with a publicly trusted certificate. It runs on another of the operator's servers, also in Hong Kong.
  - Your browser connects to nginx on that server. nginx passes the requests to the web backend on the same machine, and the backend passes them over an encrypted connection to the control server, which it recognizes by its fingerprint, as devices do.
  - That server is also run by the operator, and requests from the web app are decrypted there, as they are on the control server.
  - The page for signing a device in through a browser (`/device-login`) goes the same way. The web backend passes it to the control server unchanged, and handles the email address and code you enter, as with the web app's own sign-in.
  - nginx keeps no access log, because invitation codes appear in paths. Its error log only has the problems it cannot handle itself.
  - The web pages only connect to this server and load nothing from other sites: no outside fonts and no analytics.
- **How devices reach the control server**: current versions try three ways in, in order, and move to the next one only when the one before fails.
  - The control server's own address, as before.
  - A forwarder on another of the operator's servers (the one with the download site). It only passes the encrypted connection on to the control server as it is: the encryption ends at the control server, and the device still checks the control server's fingerprint. This server sees your IP address, how long you stay connected and how much is sent, but not the content. It does not record your IP address.
  - Cloudflare, used only when both of the above fail. On this path the encryption ends at Cloudflare, so Cloudflare sees what passes between the device and the control server: your email address, device names, the networks you set up and the credentials used to sign in. It handles them under its own privacy policy. The control server takes these connections through a Cloudflare tunnel, without opening more ports to the internet, and gets your IP address from the `CF-Connecting-IP` header that Cloudflare adds.
- **Public relays through Cloudflare**: when a current version cannot connect to a public relay directly, it connects through a Cloudflare tunnel.
  - Cloudflare cannot see the content: what passes through a relay is packets the devices encrypted, and the keys are only on the devices at both ends.
  - It sees your device's IP address, when it connects, whom it sends to (only devices' public keys, not names) and how much it sends. On a direct connection, only the relay, that is the operator, sees these.
  - The relay gets your IP address from the `CF-Connecting-IP` header that Cloudflare adds, so its admission checks and its log see the same as on a direct connection.

## 7. How long things are kept

| What | How long |
| --- | --- |
| Account | Until it is deleted (section 8) |
| Device | Until it is deleted: by its owner, together with the account, or when someone signs in on the same computer and the new device replaces the old one. Devices that signed out, or whose sign-in expired (not back for 180 days), are kept too, so that the same person gets the same address when signing in again. But the addresses and ports a device reported are cleared at once when it signs out, and at the daily cleanup when its sign-in has expired |
| A device's Deneb address | Not given to anyone else until 30 days after the device is deleted |
| LANs, members, services, refusals | Until they are deleted, left or turned off |
| Shares | Once taken back or given up, the record is kept 30 more days, then deleted. A share just taken back is not deleted during the few seconds it can still be undone |
| Invitations | Used ones are kept 30 more days; expired or withdrawn ones, 30 days past their end; then they are deleted. They are deleted with their LAN |
| Relay traffic | By day, for this month and the two before; older days are deleted. Deleted with the relay |
| Kicks | Kept while in effect; once undone, kept 30 more days, then deleted (the relay's card also only shows those undone in the last 30 days). Deleted with the relay, or with the kicked device |
| Machine facts a relay reports | As long as the relay; each new report replaces the old one |
| Results of checking a relay from the internet | The latest only, in the server's memory; gone after a restart |
| Key hashes of deleted or uninstalled relays | 30 days, then deleted |
| Relays a device cannot reach | With the device; each report replaces the old one, and a relay is removed once the device reaches it |
| Sign-in codes | 10 minutes in memory; void once used |
| Devices signing in | At most an hour, in memory |
| Web sign-ins | 30 days, then deleted; deleted at once when you sign out |
| Server logs | A file a day, kept 30 days. The container's own log rolls over by size (10 MB a file, 3 files); old logs saved at upgrades are also kept only 30 days |
| Backups | One a day, kept 7 days; before that one a week, kept 8 weeks. Copies of the whole data folder taken before upgrades (without the logs and the daily backups): the last 10, for two months at most. Something you delete stays in backups until those backups are replaced, about two months at most |
| Alerts | The last 200, in the server's memory |
| Admin records | 1 year, then deleted. The names and email addresses of deleted accounts in them stay until each record expires |

The items kept "30 more days" or for three months are cleaned up once a day by the server, together with the daily backup. The log only records how many items were deleted.

## 8. How you can delete things

- **Delete your account**: deletes the account and everything under it:
  - your devices; the ones online are signed out at once;
  - your place in each LAN, and the LANs you created that only you are in;
  - the shares you gave and received; the invitations you sent, received or used; services; refusals;
  - your own relays;
  - your sign-ins in browsers.

  Two more cases:
  - A LAN you created still has other people in it: you first choose whether to hand it over to the member who joined first, or to delete it altogether.
  - Your relay is being used by others: it is deleted all the same, and they move to the public relays. A relay container installed on your own machine has to be stopped by you.

  Delete account is on the account card, in the app and on the web. You can also ask the operator to do it for you. Test accounts (the ones in the server's settings) cannot be deleted, because the server creates them again each time it starts.

  When the operator deletes your account at your request, an email is sent to your address saying that it was deleted at your request. The email does not say which admin did it; that goes into the admin records.
- **Delete a device**: the owner of a device can delete it; if it is online, it is signed out at once. Its place in each LAN, its shares, services, refusals and kicks are deleted with it. Delete device is in My devices, in the app and on the web.
- **Sign out**: after a device signs out, it is no longer in anyone's network map, and the addresses and ports it reported are cleared at once. The server keeps the device, so that it gets the same address when you sign in again. To remove it completely, delete the device.
- **Other things you can do**:
  - leave a LAN; if you own it, delete the whole LAN;
  - take back a share;
  - delete your own relay;
  - sign out on the web.
- What you delete stays in backups until those backups are replaced, about two months at most. What was already written to the log stays up to 30 more days (section 7).

## 9. What the operator can see

- The operator runs the servers and can technically see all the records and logs on them, which are the ones listed in section 2: email addresses, device names, addresses, LANs, shares and so on.
- The admin commands the operator uses day to day list devices and their state, who is signed in in a browser, alerts, backups and the end of the log. The operations page shows the load on the public relays and the 5 people who used them most.
- **The web admin console** is only for admins of the deployment. It shows on the web what the admin commands list:
  - People: name, email address, when they joined, how many devices they have, whether they are online, how many seats they use and their limit, how many browsers they are signed in on, how many LANs they are in, how many they created, and how many relays they own;
  - Devices: name, kind, operating system, engine version, whether it is online, when its sign-in expires, the address Deneb gave it, and its owner;
  - Test devices waiting for sign-in approval, alerts, backups, today's and yesterday's server log (masked as described under "Logs" in section 2), and the admin records;
  - The load on the public relays, and the 5 people who used them most.
- The web admin console does not show:
  - the public addresses, local addresses and ports that devices report;
  - private keys, tokens or codes;
  - who connects with whom, or how much passes between them (the public relays page only shows how much in total each of the 5 heaviest users used this month);
  - who is in a LAN, which services are open or what is shared with whom. For LANs it only shows how many there are.

  The one exception is the card for deleting someone's account. To ask whom to hand them over to, it shows the names of the LANs that person created that still have other people in them, and the name of the member who joined each one first. This is the only place in the console where names of LANs and members appear.
- When someone asks, the operator can delete their account for them (in the web admin console or with the admin commands), under the same rules as deleting it yourself; afterwards an email is sent to that person (section 8). Every change an admin makes goes into the admin records (section 2).
- The operator knows the codes of test accounts, which are in the server's settings file.
- The operator cannot see:
  - what your devices send each other (it is end-to-end encrypted; the servers do not handle it, and relays cannot decrypt it);
  - the private keys on your devices;
  - the session tokens of your web sign-ins (the server only keeps their hashes).
