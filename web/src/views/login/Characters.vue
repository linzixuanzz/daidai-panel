<script setup lang="ts">
import { computed } from "vue";

export type CharacterMood =
  | "idle"
  | "typing"
  | "password"
  | "peek"
  | "success"
  | "error";

const props = defineProps<{
  mouseX: number;
  mouseY: number;
  mood: CharacterMood;
}>();

const px = computed(() => props.mouseX * 5);
const py = computed(() => props.mouseY * 4);

const coverEyes = computed(() => props.mood === "password");
const lookAway = computed(() => props.mood === "peek");
const smile = computed(() => props.mood === "success");
const sad = computed(() => props.mood === "error");

const headTilt = computed(() => (lookAway.value ? -25 : props.mouseX * 3));
const headShift = computed(() => (lookAway.value ? -20 : 0));
const bodyTilt = computed(() => (lookAway.value ? -8 : props.mouseX * 2));

const blueBody = computed(
  () => `translate(115, 25) rotate(${bodyTilt.value * 0.7}, 60, 150)`,
);
const blueFace = computed(
  () =>
    `translate(${headShift.value * 0.8}, 0) rotate(${headTilt.value * 0.3}, 60, 70)`,
);

const blackBody = computed(
  () => `translate(215, 115) rotate(${bodyTilt.value * 0.4}, 48, 105)`,
);
const blackFace = computed(
  () =>
    `translate(${headShift.value * 0.6}, 0) rotate(${headTilt.value * 0.25}, 48, 55)`,
);

const yellowBody = computed(
  () => `translate(275, 200) rotate(${bodyTilt.value * 0.5}, 55, 105)`,
);
const yellowFace = computed(
  () =>
    `translate(${headShift.value * 0.5}, 0) rotate(${headTilt.value * 0.35}, 55, 50)`,
);

const orangeBody = computed(
  () => `translate(50, 210) rotate(${bodyTilt.value * 0.3}, 140, 100)`,
);
const orangeFace = computed(
  () =>
    `translate(${headShift.value * 0.3}, 0) rotate(${headTilt.value * 0.2}, 140, 120)`,
);
</script>

<template>
  <svg
    viewBox="0 0 420 410"
    width="100%"
    height="100%"
    style="overflow: visible"
  >
    <g class="char-body" :transform="blueBody">
      <rect x="0" y="0" width="120" height="290" rx="20" fill="#1890FF" />
      <g class="char-face" :transform="blueFace">
        <template v-if="coverEyes">
          <line
            :x1="25"
            :y1="68"
            :x2="45"
            :y2="68"
            stroke="#fff"
            :stroke-width="6"
            stroke-linecap="round"
          />
          <line
            :x1="75"
            :y1="68"
            :x2="95"
            :y2="68"
            stroke="#fff"
            :stroke-width="6"
            stroke-linecap="round"
          />
        </template>
        <template v-else>
          <circle cx="35" cy="68" r="10" fill="#fff" />
          <circle
            :cx="35 + px * 0.9"
            :cy="68 + py * 0.9"
            r="4.5"
            fill="#1a1a1a"
          />
          <circle cx="85" cy="68" r="10" fill="#fff" />
          <circle
            :cx="85 + px * 0.9"
            :cy="68 + py * 0.9"
            r="4.5"
            fill="#1a1a1a"
          />
        </template>
        <path
          v-if="smile"
          d="M 35,108 Q 60,132 85,108"
          fill="none"
          stroke="#fff"
          stroke-width="3"
          stroke-linecap="round"
        />
        <path
          v-else-if="sad"
          d="M 35,125 Q 60,105 85,125"
          fill="none"
          stroke="#fff"
          stroke-width="3"
          stroke-linecap="round"
        />
        <line
          v-else
          x1="40"
          y1="112"
          x2="80"
          y2="112"
          stroke="#fff"
          stroke-width="3"
          stroke-linecap="round"
        />
      </g>
    </g>

    <g class="char-body" :transform="blackBody">
      <!-- 补暗色可见描边，避免黑色小人在呆呆/纸墨/素白暗色底上融进背景隐身 -->
      <rect
        x="0"
        y="0"
        width="95"
        height="210"
        rx="16"
        fill="#2D2D2D"
        stroke="var(--dd-border-strong)"
        stroke-width="2"
      />
      <g class="char-face" :transform="blackFace">
        <template v-if="coverEyes">
          <line
            :x1="19"
            :y1="55"
            :x2="37"
            :y2="55"
            stroke="#fff"
            :stroke-width="5.4"
            stroke-linecap="round"
          />
          <line
            :x1="58"
            :y1="55"
            :x2="76"
            :y2="55"
            stroke="#fff"
            :stroke-width="5.4"
            stroke-linecap="round"
          />
        </template>
        <template v-else>
          <circle cx="28" cy="55" r="9" fill="#fff" />
          <circle
            :cx="28 + px * 0.8"
            :cy="55 + py * 0.8"
            r="4.05"
            fill="#1a1a1a"
          />
          <circle cx="67" cy="55" r="9" fill="#fff" />
          <circle
            :cx="67 + px * 0.8"
            :cy="55 + py * 0.8"
            r="4.05"
            fill="#1a1a1a"
          />
        </template>
        <path
          v-if="smile"
          d="M 30,90 Q 48,110 66,90"
          fill="none"
          stroke="#fff"
          stroke-width="3"
          stroke-linecap="round"
        />
        <path
          v-else-if="sad"
          d="M 30,105 Q 48,88 66,105"
          fill="none"
          stroke="#fff"
          stroke-width="3"
          stroke-linecap="round"
        />
        <line
          v-else
          x1="33"
          y1="95"
          x2="62"
          y2="95"
          stroke="#fff"
          stroke-width="2.5"
          stroke-linecap="round"
        />
      </g>
    </g>

    <g class="char-body" :transform="yellowBody">
      <!-- 底部延长至局部 y=210（全局 200+210=410），与橙色小人齐平落地不再悬空 -->
      <path d="M 0,210 L 0,140 Q 0,-15 55,-15 Q 110,-15 110,140 L 110,210 Z" fill="#F5C542" />
      <g class="char-face" :transform="yellowFace">
        <template v-if="coverEyes">
          <line
            :x1="22"
            :y1="55"
            :x2="38"
            :y2="55"
            stroke="#333"
            :stroke-width="4.8"
            stroke-linecap="round"
          />
          <line
            :x1="72"
            :y1="55"
            :x2="88"
            :y2="55"
            stroke="#333"
            :stroke-width="4.8"
            stroke-linecap="round"
          />
        </template>
        <template v-else>
          <circle cx="30" cy="55" r="8" fill="#1a1a1a" />
          <circle :cx="30 + px * 0.7" :cy="55 + py * 0.7" r="3.6" fill="#fff" />
          <circle cx="80" cy="55" r="8" fill="#1a1a1a" />
          <circle :cx="80 + px * 0.7" :cy="55 + py * 0.7" r="3.6" fill="#fff" />
        </template>
        <path
          v-if="smile"
          d="M 33,88 Q 55,108 77,88"
          fill="none"
          stroke="#333"
          stroke-width="2.5"
          stroke-linecap="round"
        />
        <path
          v-else-if="sad"
          d="M 33,102 Q 55,85 77,102"
          fill="none"
          stroke="#333"
          stroke-width="2.5"
          stroke-linecap="round"
        />
        <line
          v-else
          x1="38"
          y1="92"
          x2="72"
          y2="92"
          stroke="#333"
          stroke-width="2.5"
          stroke-linecap="round"
        />
      </g>
    </g>

    <g class="char-body" :transform="orangeBody">
      <path d="M 0,200 A 140,140 0 0,1 280,200 L 0,200 Z" fill="#F5811F" />
      <g class="char-face" :transform="orangeFace">
        <template v-if="coverEyes">
          <line
            :x1="74"
            :y1="118"
            :x2="96"
            :y2="118"
            stroke="#333"
            :stroke-width="6.6"
            stroke-linecap="round"
          />
          <line
            :x1="184"
            :y1="118"
            :x2="206"
            :y2="118"
            stroke="#333"
            :stroke-width="6.6"
            stroke-linecap="round"
          />
        </template>
        <template v-else>
          <circle cx="85" cy="118" r="11" fill="#1a1a1a" />
          <circle :cx="85 + px" :cy="118 + py" r="4.95" fill="#fff" />
          <circle cx="195" cy="118" r="11" fill="#1a1a1a" />
          <circle :cx="195 + px" :cy="118 + py" r="4.95" fill="#fff" />
        </template>
        <path
          v-if="smile"
          d="M 105,160 Q 140,192 175,160"
          fill="none"
          stroke="#333"
          stroke-width="4"
          stroke-linecap="round"
        />
        <path
          v-else-if="sad"
          d="M 105,180 Q 140,155 175,180"
          fill="none"
          stroke="#333"
          stroke-width="4"
          stroke-linecap="round"
        />
        <ellipse v-else cx="140" cy="165" rx="7" ry="6" fill="#333" />
      </g>
    </g>
  </svg>
</template>

<style scoped>
:deep(.char-body) {
  transition: transform 0.4s cubic-bezier(0.25, 0.46, 0.45, 0.94);
}
:deep(.char-face) {
  transition: transform 0.25s ease-out;
}
</style>
