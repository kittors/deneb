// M3-20 网页上的"等同意" (DESIGN §6.7, §14.4), recorded on the real page
// against a development control server filled by web/probe/devworld:
// kittors's 周末联机, where 阿杰's 树莓派 and 阿明 wait for him. He lets the
// device in (a click: it fills), then 阿明 (his dashed ball fills), and
// opens the LAN's card: its members, the two switches of who waits, and
// "成员只用分到的设备". Loaded only with ?record=wait.
import './rec.js';
const rest = [760, 820];
(window                                      ).RECORDING = {
  title: 'M3-20-网页上的等同意',
  duration: 13600,
  actions: [
    { at: 800, shot: '等你同意', clean: true },
    { at: 1100, glide: { sel: '.dev|阿杰的树莓派|.tile' }, dur: 800 },
    { at: 2100, down: true },
    { at: 2180, up: true },
    { at: 2400, glide: rest, dur: 600 },
    { at: 3400, shot: '同意了设备', clean: true },
    { at: 3700, glide: { sel: '.stack .sball|阿明' }, dur: 900 },
    { at: 4900, shot: '悬停看得到拒绝' },
    { at: 5200, down: true },
    { at: 5280, up: true },
    { at: 5500, glide: rest, dur: 600 },
    { at: 6500, shot: '同意了阿明', clean: true },
    { at: 8600, glide: { sel: '.head .title' }, dur: 800 },
    { at: 9600, down: true },
    { at: 9680, up: true },
    { at: 9900, glide: rest, dur: 700 },
    { at: 11200, shot: '局域网卡片', clean: true },
    { at: 13200, shot: '收起提示条', clean: true },
  ],
};
