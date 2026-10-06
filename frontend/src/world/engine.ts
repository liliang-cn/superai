import * as THREE from "three";
import { OrbitControls } from "three/examples/jsm/controls/OrbitControls.js";
import { CSS2DObject, CSS2DRenderer } from "three/examples/jsm/renderers/CSS2DRenderer.js";
import { C, box, ease, flower, hash01, lamp, mat, plot, rbox, spark, tree, woodFence } from "./kit";

// The world behind the whole app: one scene, a campus of districts (one per
// page), an isometric camera that flies to whichever page is open. Districts
// build themselves from the kit and are handed the app's data; the engine
// keeps the camera, the labels, picking and the clock.

export type Picked = { district: string; id: string; kind: string; title: string };

export interface Label {
  set(name: string, status?: string, state?: "work" | "idle" | "wait" | "bad"): void;
  show(on: boolean): void;
  remove(): void;
  object: CSS2DObject;
}

export interface DistrictCtx {
  /** The district's own group, already standing on its plot. */
  group: THREE.Group;
  /** A pill over `obj`. `lift` is how far above its origin. */
  label(obj: THREE.Object3D, name: string, status?: string, lift?: number): Label;
  /** Makes `obj` (and everything in it) selectable as `pick`. */
  pickable(obj: THREE.Object3D, pick: Omit<Picked, "district">): void;
  /** Called every frame with seconds since the last one and since start. */
  tick(fn: (dt: number, t: number) => void): void;
  /** A ring off `pos` (world space). */
  spark(pos: THREE.Vector3, color?: number): void;
  /** Where another district is, for things that travel between them. */
  anchor(key: string): THREE.Vector3;
  reduced: boolean;
}

export interface District<D> {
  key: string;
  title: string;
  /** Plot centre and size, in world units. */
  at: [number, number];
  size: [number, number];
  /** How close the camera comes when this page is open (1 = whole plot). */
  zoom?: number;
  /** No plot, fence or name: the district is the whole scene. */
  bare?: boolean;
  build(ctx: DistrictCtx): { update(data: D): void };
}

const ELEV = THREE.MathUtils.degToRad(35);
const AZIM = THREE.MathUtils.degToRad(45);
// A real perspective camera, free to turn all the way round. "Zoom" is kept
// as a number pages can speak in: the camera stands BASE / zoom away.
const BASE = 104;
const FOV = 30;

export class WorldEngine<D> {
  readonly scene = new THREE.Scene();
  private renderer: THREE.WebGLRenderer;
  private labels: CSS2DRenderer;
  private camera: THREE.PerspectiveCamera;
  private controls: OrbitControls;
  private ticks: ((dt: number, t: number) => void)[] = [];
  private updates = new Map<string, (d: D) => void>();
  private districts = new Map<string, District<D>>();
  private labelOwners: { el: HTMLElement; obj: CSS2DObject; district: string; on: boolean }[] = [];
  private pickables: THREE.Object3D[] = [];
  private sparks: THREE.Mesh[] = [];
  private brackets: THREE.Group;
  private selected: THREE.Object3D | null = null;
  private onPick: (p: Picked | null) => void = () => {};
  private raf = 0;
  private last = performance.now();
  private start = performance.now();
  private flight: { from: THREE.Vector3; to: THREE.Vector3; z0: number; z1: number; t: number } | null = null;
  private focus = "";
  private inset = 0;
  private size = { w: 1, h: 1 };
  private ro: ResizeObserver;
  readonly reduced = window.matchMedia("(prefers-reduced-motion: reduce)").matches;

  constructor(private host: HTMLElement) {
    this.renderer = new THREE.WebGLRenderer({ antialias: true, powerPreference: "high-performance" });
    this.renderer.setPixelRatio(Math.min(window.devicePixelRatio, 2));
    this.renderer.shadowMap.enabled = true;
    this.renderer.shadowMap.type = THREE.PCFSoftShadowMap;
    this.renderer.outputColorSpace = THREE.SRGBColorSpace;
    this.renderer.toneMapping = THREE.NoToneMapping;
    host.appendChild(this.renderer.domElement);
    this.labels = new CSS2DRenderer();
    this.labels.domElement.className = "wl-labels";
    host.appendChild(this.labels.domElement);

    this.camera = new THREE.PerspectiveCamera(FOV, 1, 0.5, 1200);
    this.camera.position.set(Math.cos(ELEV) * Math.sin(AZIM), Math.sin(ELEV), Math.cos(ELEV) * Math.cos(AZIM)).multiplyScalar(BASE);
    this.controls = new OrbitControls(this.camera, this.renderer.domElement);
    this.controls.enableDamping = true;
    this.controls.dampingFactor = 0.08;
    // Left drag turns the world, right drag slides it, the wheel or a pinch
    // comes closer; the camera may look straight down or skim the ground.
    this.controls.minPolarAngle = 0.08;
    this.controls.maxPolarAngle = 1.6;
    this.controls.minDistance = 3;
    this.controls.maxDistance = 150;
    this.controls.rotateSpeed = 0.6;
    // A trackpad's two-finger scroll arrives as a stream of wheel events:
    // slow it down, and zoom toward what the pointer is on.
    this.controls.zoomSpeed = 0.45;
    this.controls.zoomToCursor = true;
    this.controls.screenSpacePanning = false;
    this.controls.mouseButtons = { LEFT: THREE.MOUSE.ROTATE, MIDDLE: THREE.MOUSE.DOLLY, RIGHT: THREE.MOUSE.PAN };
    this.controls.touches = { ONE: THREE.TOUCH.ROTATE, TWO: THREE.TOUCH.DOLLY_PAN };

    this.lights();
    this.ground();
    this.brackets = this.makeBrackets();
    this.scene.add(this.brackets);

    this.bindPicking();
    window.addEventListener("keydown", this.onKey);
    this.ro = new ResizeObserver(() => this.resize());
    this.ro.observe(host);
    this.resize();
    this.loop = this.loop.bind(this);
    this.raf = requestAnimationFrame(this.loop);
  }

  private lights() {
    this.scene.add(new THREE.HemisphereLight(0xffffff, 0xc9d2de, 1.25));
    const sun = new THREE.DirectionalLight(0xffffff, 2.7);
    sun.position.set(-25, 80, 60);
    sun.castShadow = true;
    sun.shadow.mapSize.set(4096, 4096);
    const s = sun.shadow.camera as THREE.OrthographicCamera;
    s.left = -90; s.right = 90; s.top = 90; s.bottom = -90; s.near = 1; s.far = 220;
    sun.shadow.bias = -0.0004;
    sun.shadow.normalBias = 0.03;
    sun.shadow.radius = 4;
    this.scene.add(sun);
  }

  private ground() {
    // One floor to the horizon, fading into the haze: no edge to fall off.
    const haze = 0xeef1f5;
    this.scene.background = new THREE.Color(haze);
    this.scene.fog = new THREE.Fog(haze, 120, 300);
    const g = new THREE.Mesh(new THREE.PlaneGeometry(1200, 1200), mat(0xf3f5f8, { rough: 1 }));
    g.rotation.x = -Math.PI / 2;
    g.receiveShadow = true;
    this.scene.add(g);
    const grid = new THREE.GridHelper(600, 300, 0xdde2e9, 0xe6eaef);
    grid.position.y = 0.04;
    (grid.material as THREE.Material).transparent = true;
    (grid.material as THREE.Material).opacity = 0.9;
    this.scene.add(grid);
  }
  private swayers: THREE.Object3D[] = [];
  private frame = 0;

  /**
   * Labels that would land on each other give way: what is selected, then
   * what is busy, waiting or failing, then the nearest, keeps its place; the
   * rest fade. From far off a label is just its name.
   */
  private declutter() {
    const far = this.camera.position.distanceTo(this.controls.target) > 95;
    this.labels.domElement.classList.toggle("far", far);
    const rank = (el: HTMLElement) => el.classList.contains("sel") || el.classList.contains("hint") ? 0
      : el.dataset.state === "wait" || el.dataset.state === "bad" ? 1 : el.dataset.state === "work" ? 2 : 3;
    const items = this.labelOwners
      .filter((l) => l.obj.visible && l.el.style.display !== "none" && l.el.classList.contains("wl-pill"))
      .map((l) => ({ el: l.el, r: l.el.getBoundingClientRect(), k: rank(l.el), d: l.obj.getWorldPosition(new THREE.Vector3()).distanceTo(this.camera.position) }))
      .filter((x) => x.r.width > 0)
      .sort((a, b) => a.k - b.k || a.d - b.d);
    const kept: DOMRect[] = [];
    for (const x of items) {
      const hit = kept.some((r) => x.r.left < r.right - 4 && x.r.right > r.left + 4 && x.r.top < r.bottom - 2 && x.r.bottom > r.top + 2);
      x.el.classList.toggle("hidden", hit);
      if (!hit) kept.push(x.r);
    }
  }

  /** Adds a district: its plot, its name on the ground, and whatever it builds. */
  add(d: District<D>) {
    const group = new THREE.Group();
    group.position.set(d.at[0], 0, d.at[1]);
    if (!d.bare) this.decorate(d, group);
    this.scene.add(group);
    this.districts.set(d.key, d);
    this.buildDistrict(d, group);
  }

  private decorate(d: District<D>, group: THREE.Group) {
    const p = plot(d.size[0], d.size[1]);
    group.add(p);
    for (const [x, z] of [[-1, -1], [1, -1], [-1, 1], [1, 1]]) {
      const t = tree(hash01(d.key + x + z));
      t.scale.setScalar(0.8);
      t.position.set((x * d.size[0]) / 2 - x * 1.2, 0, (z * d.size[1]) / 2 - z * 1.2);
      group.add(t);
      this.swayers.push(t);
    }
    // Every place has a fence at the back, a lantern at the front and a few
    // flowers, so none stands bare.
    const [w, dd] = d.size;
    const back = woodFence(w - 3);
    back.position.set(0, 0, -dd / 2 + 0.6);
    const side = woodFence(dd - 3);
    side.rotation.y = Math.PI / 2;
    side.position.set(-w / 2 + 0.6, 0, 0);
    group.add(back, side);
    const l = lamp();
    l.position.set(w / 2 - 1.4, 0, dd / 2 - 1.4);
    l.rotation.y = Math.PI;
    group.add(l);
    const petals = [0xffffff, 0xffd8e0, 0xffe9a8, 0xcfe3ff];
    for (let i = 0; i < 9; i++) {
      const s = hash01(d.key + "f" + i);
      const f = flower(petals[i % petals.length], s);
      f.position.set(-w / 2 + 1.6 + s * 3.2, 0, dd / 2 - 1.2 - hash01(d.key + "g" + i) * 2.6);
      group.add(f);
      this.swayers.push(f);
    }
    const title = document.createElement("div");
    title.className = "wl-plot";
    title.textContent = d.title;
    const tObj = new CSS2DObject(title);
    tObj.position.set(-d.size[0] / 2 + 1.2, 0.2, d.size[1] / 2 - 0.4);
    group.add(tObj);
    // Only the open page's district says its name; the rest are scenery.
    this.labelOwners.push({ el: title, obj: tObj, district: "title:" + d.key, on: true });
  }

  private buildDistrict(d: District<D>, group: THREE.Group) {

    const ctx: DistrictCtx = {
      group,
      reduced: this.reduced,
      label: (obj, name, status, lift = 4) => this.label(d.key, obj, name, status, lift),
      pickable: (obj, pick) => {
        obj.traverse((o) => { o.userData.pick = { ...pick, district: d.key }; o.userData.pickRoot = obj; });
        this.pickables.push(obj);
      },
      tick: (fn) => this.ticks.push(fn),
      spark: (pos, color) => this.spark(pos, color),
      anchor: (key) => {
        const o = this.districts.get(key);
        return o ? new THREE.Vector3(o.at[0], 0, o.at[1]) : new THREE.Vector3();
      },
    };
    const built = d.build(ctx);
    this.updates.set(d.key, built.update);
  }

  private label(district: string, obj: THREE.Object3D, name: string, status = "", lift = 4): Label {
    const el = document.createElement("div");
    el.className = "wl-pill";
    const n = document.createElement("b");
    const s = document.createElement("span");
    const dot = document.createElement("i");
    el.append(dot, n, s);
    const o = new CSS2DObject(el);
    o.position.set(0, lift, 0);
    obj.add(o);
    const owner = { el, obj: o, district, on: true };
    this.labelOwners.push(owner);
    const set: Label["set"] = (nm, st = "", state = "idle") => {
      n.textContent = nm;
      s.textContent = st;
      s.style.display = st ? "" : "none";
      el.dataset.state = state;
    };
    set(name, status);
    this.applyFocus();
    return {
      set, object: o,
      show: (on) => { owner.on = on; this.applyFocus(); },
      remove: () => {
        o.removeFromParent();
        el.remove();
        this.labelOwners = this.labelOwners.filter((x) => x !== owner);
      },
    };
  }

  private applyFocus() {
    const near = this.focus === "home" ? ["home", "hive", "cluster"] : [this.focus, "home", "cluster"];
    // The label renderer shows or hides each label by its object's own
    // visibility, so that is what is set.
    for (const l of this.labelOwners) l.obj.visible = l.on && (near.includes(l.district) || l.district === "title:" + this.focus);
  }

  update(data: D) {
    for (const u of this.updates.values()) {
      try { u(data); } catch (e) { console.warn("world: a district failed to update", e); }
    }
  }

  /** Flies to a page's district. */
  fly(key: string, instant = false) {
    const d = this.districts.get(key) ?? this.districts.get("home");
    if (!d) return;
    this.focus = d.key;
    this.applyFocus();
    const to = new THREE.Vector3(d.at[0], 0, d.at[1]);
    const zoom = d.zoom ?? 1;
    if (instant || this.reduced) {
      this.place(to, zoom);
      return;
    }
    this.flight = { from: this.controls.target.clone(), to, z0: this.zoomNow(), z1: zoom, t: 0 };
  }

  private lens: { x: number; z: number; zoom: number } | null = null;
  /** Flies to a point and zoom (a lens on a one-scene world). */
  look(x: number, z: number, zoom: number, focus: string, instant = false) {
    this.focus = focus;
    this.lens = { x, z, zoom };
    this.applyFocus();
    const to = new THREE.Vector3(x, 0, z);
    if (instant || this.reduced) { this.place(to, zoom); return; }
    // Back to the standing view: looking down at the page's place from the
    // usual corner, whatever angle the camera was turned to.
    const dir = new THREE.Vector3(Math.cos(ELEV) * Math.sin(AZIM), Math.sin(ELEV), Math.cos(ELEV) * Math.cos(AZIM));
    this.flight = null;
    this.glide = { p0: this.camera.position.clone(), p1: to.clone().add(dir.multiplyScalar(BASE / zoom)), t0: this.controls.target.clone(), t1: to, t: 0 };
  }

  /** Back to the open page's view, as the camera buttons' home does. */
  home() { this.glide = null; if (this.lens) this.look(this.lens.x, this.lens.z, this.lens.zoom, this.focus); else this.fly(this.focus); }
  zoomBy(f: number) { const z = this.zoomNow(); this.flight = { from: this.controls.target.clone(), to: this.controls.target.clone(), z0: z, z1: THREE.MathUtils.clamp(z * f, BASE / 150, BASE / 8), t: 0 }; }
  private zoomNow() { return BASE / Math.max(1, this.camera.position.distanceTo(this.controls.target)); }
  rotateBy(rad: number) {
    const off = this.camera.position.clone().sub(this.controls.target);
    off.applyAxisAngle(new THREE.Vector3(0, 1, 0), rad);
    this.camera.position.copy(this.controls.target).add(off);
    this.controls.update();
  }

  private place(target: THREE.Vector3, zoom: number) {
    const dir = this.camera.position.clone().sub(this.controls.target).normalize();
    this.controls.target.copy(target);
    this.camera.position.copy(target).add(dir.multiplyScalar(BASE / Math.max(0.05, zoom)));
    this.controls.update();
  }

  /** How much of the left edge a card covers, so the district is centred in
   *  what can be seen. */
  setInset(px: number) {
    this.inset = px;
    this.resize();
  }

  pickHandler(fn: (p: Picked | null) => void) { this.onPick = fn; }

  private callout: { card: HTMLElement; line: SVGLineElement } | null = null;
  private selBox = { top: 2, sx: 1.6, sz: 1.6, cx: 0, cz: 0 };
  private lastCard = "";
  /** A card that follows the selected thing, tied to it by a thin line. */
  setCallout(card: HTMLElement | null, line: SVGLineElement | null) {
    this.callout = card && line ? { card, line } : null;
  }
  private placeCallout() {
    const c = this.callout;
    if (!c) return;
    if (!this.selected) { c.line.style.display = "none"; return; }
    const at = this.selected.getWorldPosition(new THREE.Vector3());
    const top = new THREE.Vector3(at.x + this.selBox.cx, at.y + this.selBox.top, at.z + this.selBox.cz).project(this.camera);
    const x = Math.round((top.x + 1) / 2 * this.size.w), y = Math.round((1 - top.y) / 2 * this.size.h);
    const cw = c.card.offsetWidth, ch = c.card.offsetHeight;
    // To the right of the thing when there is room, else to its left.
    let left = x + 70, topPx = y - ch / 2 - 30;
    if (left + cw > this.size.w - 90) left = x - 70 - cw;
    left = Math.max(this.inset + 12, left);
    topPx = Math.max(52, Math.min(this.size.h - ch - 96, topPx));
    // Only touch the page when something actually moved.
    const sig = `${Math.round(left)},${Math.round(topPx)},${x},${y}`;
    if (sig === this.lastCard) return;
    this.lastCard = sig;
    c.card.style.transform = `translate(${Math.round(left)}px, ${Math.round(topPx)}px)`;
    const ex = left > x ? left : left + cw;
    c.line.setAttribute("x1", String(x)); c.line.setAttribute("y1", String(y));
    c.line.setAttribute("x2", String(ex)); c.line.setAttribute("y2", String(Math.max(topPx + 18, Math.min(topPx + ch - 18, y))));
    c.line.style.display = "";
  }

  private glide: { p0: THREE.Vector3; p1: THREE.Vector3; t0: THREE.Vector3; t1: THREE.Vector3; t: number } | null = null;
  /** Brings the camera square in front of a screen, close enough to read. */
  faceScreen(mesh: THREE.Object3D) {
    const center = mesh.getWorldPosition(new THREE.Vector3());
    const normal = new THREE.Vector3(0, 0, 1).applyQuaternion(mesh.getWorldQuaternion(new THREE.Quaternion())).normalize();
    const w = (mesh.userData.w as number) || 3;
    const dist = (w * 0.5) / Math.tan(THREE.MathUtils.degToRad(FOV / 2)) / Math.min(1.6, this.camera.aspect) * 1.35;
    const to = center.clone().add(normal.multiplyScalar(dist));
    this.flight = null;
    this.glide = { p0: this.camera.position.clone(), p1: to, t0: this.controls.target.clone(), t1: center, t: 0 };
  }

  /** Lights a thing without selecting it, as typing its name does. */
  private hinted: THREE.Object3D | null = null;
  hint(kind: string, id: string) {
    const obj = id ? this.pickables.find((o) => o.userData.pick?.kind === kind && o.userData.pick?.id === id) ?? null : null;
    const mark = (o: THREE.Object3D | null, on: boolean) => o?.traverse((c) => { if (c instanceof CSS2DObject) c.element.classList.toggle("hint", on); });
    mark(this.hinted, false);
    this.hinted = obj;
    mark(obj, true);
  }

  /** Where a thing is on screen, in CSS pixels (for scripted demos). */
  screenOf(kind: string, id: string): { x: number; y: number } | null {
    const obj = this.pickables.find((o) => o.userData.pick?.kind === kind && o.userData.pick?.id === id);
    if (!obj) return null;
    const b = new THREE.Box3().setFromObject(obj);
    const p = b.getCenter(new THREE.Vector3()).project(this.camera);
    const r = this.renderer.domElement.getBoundingClientRect();
    return { x: r.left + ((p.x + 1) / 2) * r.width, y: r.top + ((1 - p.y) / 2) * r.height };
  }

  /** Faces the first screen on a thing (for scripted demos). */
  faceScreenOf(kind: string, id: string) {
    const obj = this.pickables.find((o) => o.userData.pick?.kind === kind && o.userData.pick?.id === id);
    let sc: THREE.Object3D | null = null;
    obj?.traverse((o) => { if (!sc && o.userData.isScreen) sc = o; });
    if (sc) this.faceScreen(sc);
    return !!sc;
  }

  /** Turns the camera slowly round its target (for scripted demos). */
  private spin = 0;
  setSpin(radPerSec: number) { this.spin = radPerSec; }

  /** Selects the thing a page pointed at and brings the camera over it. */
  focusOn(kind: string, id: string): boolean {
    this.prune();
    const obj = this.pickables.find((o) => o.userData.pick?.kind === kind && o.userData.pick?.id === id);
    if (!obj) return false;
    const pick = obj.userData.pick as Picked;
    if (pick.district !== this.focus) { this.focus = pick.district; this.applyFocus(); }
    this.select(obj);
    const c = new THREE.Box3().setFromObject(obj).getCenter(new THREE.Vector3());
    c.y = 0;
    const d = this.districts.get(pick.district);
    this.flight = { from: this.controls.target.clone(), to: c, z0: this.zoomNow(), z1: Math.max(this.zoomNow(), (d?.zoom ?? 1.6) * 1.25), t: 0 };
    this.onPick(pick);
    return true;
  }

  select(obj: THREE.Object3D | null) {
    // The selected thing's own label turns blue, as well as the brackets.
    const mark = (o: THREE.Object3D | null, on: boolean) => o?.traverse((c) => {
      if (c instanceof CSS2DObject) c.element.classList.toggle("sel", on);
    });
    mark(this.selected, false);
    this.selected = obj;
    mark(obj, true);
    this.brackets.visible = !!obj;
    // Measured once: a robot's sway and blink change its box every frame, and
    // a card that followed the box would shake.
    if (obj) {
      const b = new THREE.Box3().setFromObject(obj);
      const at = obj.getWorldPosition(new THREE.Vector3());
      this.selBox = { top: b.max.y - at.y, sx: Math.max(1.6, (b.max.x - b.min.x) / 2 + 0.6), sz: Math.max(1.6, (b.max.z - b.min.z) / 2 + 0.6), cx: (b.min.x + b.max.x) / 2 - at.x, cz: (b.min.z + b.max.z) / 2 - at.z };
    }
    this.lastCard = "";
  }

  private makeBrackets() {
    const g = new THREE.Group();
    const m = new THREE.MeshBasicMaterial({ color: 0x2f5bea, transparent: true, opacity: 0.9 });
    for (let i = 0; i < 4; i++) {
      const c = new THREE.Group();
      const a = new THREE.Mesh(new THREE.BoxGeometry(1.3, 0.08, 0.24), m);
      a.position.x = 0.65;
      const b = new THREE.Mesh(new THREE.BoxGeometry(0.24, 0.08, 1.3), m);
      b.position.z = 0.65;
      c.add(a, b);
      c.rotation.y = (-i * Math.PI) / 2;
      g.add(c);
    }
    g.visible = false;
    g.userData.mat = m;
    return g;
  }

  private bindPicking() {
    const ray = new THREE.Raycaster();
    const v = new THREE.Vector2();
    let down: { x: number; y: number } | null = null;
    const el = this.renderer.domElement;
    el.addEventListener("pointerdown", (e) => { down = { x: e.clientX, y: e.clientY }; });
    el.addEventListener("pointerup", (e) => {
      if (!down || Math.hypot(e.clientX - down.x, e.clientY - down.y) > 5 || e.button !== 0) return;
      down = null;
      const r = el.getBoundingClientRect();
      v.set(((e.clientX - r.left) / r.width) * 2 - 1, -((e.clientY - r.top) / r.height) * 2 + 1);
      ray.setFromCamera(v, this.camera);
      this.prune();
      const hit = ray.intersectObjects(this.pickables, true).find((h) => h.object.userData.pick);
      if (!hit) { this.select(null); this.onPick(null); return; }
      this.select(hit.object.userData.pickRoot ?? hit.object);
      this.onPick(hit.object.userData.pick as Picked);
    });
    // Double-click a screen: go and stand in front of it.
    el.addEventListener("dblclick", (e) => {
      const r = el.getBoundingClientRect();
      v.set(((e.clientX - r.left) / r.width) * 2 - 1, -((e.clientY - r.top) / r.height) * 2 + 1);
      ray.setFromCamera(v, this.camera);
      const hit = ray.intersectObjects(this.scene.children, true).find((h) => h.object.userData.isScreen);
      if (hit) this.faceScreen(hit.object);
    });
    el.addEventListener("pointermove", (e) => {
      const r = el.getBoundingClientRect();
      v.set(((e.clientX - r.left) / r.width) * 2 - 1, -((e.clientY - r.top) / r.height) * 2 + 1);
      ray.setFromCamera(v, this.camera);
      if (this.pickables.length > 200) this.prune();
      el.style.cursor = ray.intersectObjects(this.pickables, true).some((h) => h.object.userData.pick) ? "pointer" : "";
    });
  }

  /** Drops what was made pickable but has since left the scene. */
  private prune() {
    const inScene = (o: THREE.Object3D) => { let p: THREE.Object3D | null = o; while (p) { if (p === this.scene) return true; p = p.parent; } return false; };
    this.pickables = [...new Set(this.pickables)].filter(inScene);
  }

  private spark(pos: THREE.Vector3, color = C.honey) {
    if (this.reduced) return;
    const s = spark(color);
    s.position.copy(pos);
    this.scene.add(s);
    this.sparks.push(s);
  }

  private resize() {
    const w = Math.max(1, this.host.clientWidth), h = Math.max(1, this.host.clientHeight);
    this.size = { w, h };
    this.renderer.setSize(w, h, false);
    this.renderer.domElement.style.width = w + "px";
    this.renderer.domElement.style.height = h + "px";
    this.labels.setSize(w, h);
    this.camera.aspect = w / h;
    // Centre what is looked at in the part of the window the card leaves.
    if (this.inset > 0 && this.inset < w) this.camera.setViewOffset(w, h, -this.inset / 2, 0, w, h);
    else this.camera.clearViewOffset();
    this.camera.updateProjectionMatrix();
  }

  private loop(now: number) {
    this.raf = requestAnimationFrame(this.loop);
    if (document.hidden) return;
    const dt = Math.min(0.1, (now - this.last) / 1000);
    this.last = now;
    const t = (now - this.start) / 1000;

    if (this.spin && !this.flight && !this.glide) this.rotateBy(this.spin * dt);
    if (this.glide) {
      const g = this.glide;
      g.t = Math.min(1, g.t + dt / 1.1);
      const k = ease(g.t);
      this.camera.position.lerpVectors(g.p0, g.p1, k);
      this.controls.target.lerpVectors(g.t0, g.t1, k);
      if (g.t >= 1) this.glide = null;
    }
    if (this.flight) {
      const f = this.flight;
      f.t = Math.min(1, f.t + dt / 1.1);
      const k = ease(f.t);
      const target = f.from.clone().lerp(f.to, k);
      // A flight pulls back a little in the middle, so the campus passes by.
      const dip = f.from.distanceTo(f.to) > 1 ? Math.sin(k * Math.PI) * 0.35 : 0;
      this.place(target, THREE.MathUtils.lerp(f.z0, f.z1, k) * (1 - dip));
      if (f.t >= 1) this.flight = null;
    }
    if (!this.reduced) for (const s of this.swayers) s.rotation.z = Math.sin(t * 0.8 + s.userData.sway) * 0.025;
    for (const fn of this.ticks) fn(dt, t);
    for (let i = this.sparks.length - 1; i >= 0; i--) {
      const s = this.sparks[i];
      s.userData.life += dt;
      const k = s.userData.life / 0.9;
      s.scale.setScalar(0.6 + k * 2.6);
      (s.material as THREE.MeshBasicMaterial).opacity = Math.max(0, 0.9 * (1 - k));
      if (k >= 1) { this.scene.remove(s); this.sparks.splice(i, 1); }
    }
    if (this.selected && this.brackets.visible) {
      const at = this.selected.getWorldPosition(new THREE.Vector3());
      const { sx, sz } = this.selBox;
      this.brackets.position.set(at.x + this.selBox.cx, Math.max(0.08, at.y + 0.06), at.z + this.selBox.cz);
      this.brackets.children.forEach((ch, i) => {
        const [x, z] = [[-1, -1], [1, -1], [1, 1], [-1, 1]][i];
        ch.position.set(x * sx, 0, z * sz);
      });
      (this.brackets.userData.mat as THREE.MeshBasicMaterial).opacity = 0.6 + 0.35 * Math.sin(t * 3);
    }
    this.controls.update();
    this.placeCallout();
    this.renderer.render(this.scene, this.camera);
    this.labels.render(this.scene, this.camera);
    if (++this.frame % 6 === 0) this.declutter();
  }

  private onKey = (e: KeyboardEvent) => {
    if (e.key === "Escape" && !(e.target as HTMLElement)?.closest?.("input,textarea")) this.home();
  };

  dispose() {
    window.removeEventListener("keydown", this.onKey);
    cancelAnimationFrame(this.raf);
    this.ro.disconnect();
    this.controls.dispose();
    this.renderer.dispose();
    this.renderer.domElement.remove();
    this.labels.domElement.remove();
  }
}

export { rbox };
