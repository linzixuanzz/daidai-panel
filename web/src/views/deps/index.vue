<template>
  <div ref="pageRootRef" class="deps-page dd-scroll-page dd-page-hide-heading">
    <div class="page-header dd-rise" style="--dd-i: 0">
      <div>
        <h2 class="page-title-with-icon"><el-icon><Box /></el-icon><span>依赖管理</span></h2>
        <p class="page-subtitle">
          管理运行时所需的依赖包和系统软件，确保依赖版本和任务稳定运行
        </p>
      </div>
    </div>

    <!-- Android 面具版：一键安装 Python / Node 解释器 -->
    <el-card
      v-if="androidStatus && androidStatus.supported"
      class="android-runtime-card dd-spot dd-rise" style="--dd-i: 1"
      shadow="never"
    >
      <template #header>
        <div class="android-runtime-header">
          <span>
            <el-icon><Cpu /></el-icon>
            Android 脚本运行时 <el-tag size="small" type="info">面具版</el-tag>
          </span>
          <span class="android-runtime-meta">
            架构 {{ androidStatus.arch }} · 安装目录 {{ androidStatus.bin_dir }}
            <el-tag
              v-if="androidStatus.termux_detected"
              size="small"
              type="success"
              >已检测 Termux</el-tag
            >
          </span>
        </div>
      </template>

      <div class="android-runtime-tip">
        <el-alert type="info" :closable="false" show-icon>
          面具环境没有
          apt/apk，脚本解释器需要手动安装。点击下方按钮会把运行时下载解压到
          <code>{{ androidStatus.bin_dir }}</code
          >，随后 Python/Node 脚本即可运行。 如果装了 Termux，面板也会自动识别
          <code>/data/data/com.termux/files/usr/bin</code> 里的解释器。
        </el-alert>
      </div>

      <el-row :gutter="16" class="android-runtime-grid">
        <el-col
          v-for="item in androidStatus.runtimes"
          :key="item.name"
          :xs="24"
          :sm="12"
        >
          <div class="runtime-item">
            <div class="runtime-item__head">
              <b>{{ item.name }}</b>
              <el-tag v-if="item.installed" type="success" size="small"
                >已安装</el-tag
              >
              <el-tag v-else type="warning" size="small">未安装</el-tag>
            </div>
            <div class="runtime-item__meta">
              <div v-if="item.installed">
                <div>
                  路径: <code>{{ item.path }}</code>
                </div>
                <div v-if="item.version">版本: {{ item.version }}</div>
              </div>
              <div v-else>
                <template v-if="presetFor(item.name)">
                  将下载 {{ presetFor(item.name)?.label }}（约
                  {{ presetFor(item.name)?.size_mb }}MB）
                  <div
                    v-if="presetFor(item.name)?.note"
                    class="runtime-item__note"
                  >
                    提示：{{ presetFor(item.name)?.note }}
                  </div>
                </template>
                <template v-else>
                  当前架构 {{ androidStatus.arch }} 暂无预置下载源
                </template>
              </div>
            </div>
            <div class="runtime-item__actions">
              <el-button
                v-if="!item.installed"
                type="primary"
                size="small"
                :loading="androidInstallingName === item.name"
                :disabled="!presetFor(item.name)"
                @click="installAndroidRuntime(item.name)"
              >
                一键安装
              </el-button>
              <el-button
                v-else
                size="small"
                :loading="androidInstallingName === item.name"
                @click="installAndroidRuntime(item.name)"
              >
                重新安装
              </el-button>
              <el-button
                v-if="item.installed"
                type="danger"
                size="small"
                plain
                @click="uninstallAndroidRuntime(item.name)"
              >
                移除
              </el-button>
            </div>
          </div>
        </el-col>
      </el-row>

      <div v-if="androidInstallLog.length" class="android-runtime-log">
        <div class="android-runtime-log__title">
          安装日志
          <el-button link size="small" @click="androidInstallLog = []"
            >清空</el-button
          >
        </div>
        <pre ref="androidLogRef" v-html="androidInstallLogHtml"></pre>
      </div>
    </el-card>

    <!-- 移动端工具栏（v3.3.1，issue #143）：桌面走下面 v-else 的 .deps-tabs + .toolbar，DOM 一字不动。
         三行：①搜索 + 新增依赖 + 更多菜单（勾选后整行换成批量栏）；②类型页签占满一行、贴屏幕左右边缘；
         ③只在 Python 页签出现：版本选择 + 设为默认。
         状态筛选（桌面的 el-select 与「全部 / 已安装 / 失败」分段）移动端不渲染，
         进入移动端时由 watch(isMobile) 清空，免得留下看不见也关不掉的筛选。 -->
    <div v-if="isMobile" class="deps-mobile-toolbar dd-rise" style="--dd-i: 2">
      <div v-if="selectedIds.length === 0" class="dd-mobile-toolbar">
        <el-input
          v-model="searchKeyword"
          placeholder="搜索依赖包名称..."
          clearable
          @keyup.enter="depsPage = 1"
          @clear="depsPage = 1"
        >
          <template #prefix
            ><el-icon><Search /></el-icon
          ></template>
        </el-input>
        <el-button
          type="primary"
          class="dd-icon-only-btn"
          :icon="Plus"
          aria-label="新增依赖"
          title="新增依赖"
          @click="openCreateDialog"
        />
        <!-- 与桌面 Split Button 的菜单是同一份 toolbarActionItems（含「安装 Playwright 运行环境」），
             popper-class 相同，所以菜单观感与桌面一致；「批量重装」在移动端 visible=false，已挪进批量栏。 -->
        <el-dropdown
          trigger="click"
          placement="bottom-end"
          popper-class="dd-split-button__popper"
          @command="onToolbarAction"
        >
          <el-button
            class="dd-icon-only-btn"
            :icon="ArrowDown"
            aria-label="更多操作"
            title="更多操作"
          />
          <template #dropdown>
            <el-dropdown-menu>
              <el-dropdown-item
                v-for="item in mobileToolbarMenuItems"
                :key="item.key"
                :command="item.key"
                :disabled="item.disabled"
                :divided="item.divided"
                :class="{
                  'dd-split-button__item--danger': item.danger,
                  'dd-split-button__item--success': item.success,
                }"
              >
                <el-icon v-if="item.icon"><component :is="item.icon" /></el-icon>
                <span>{{ item.label }}</span>
              </el-dropdown-item>
            </el-dropdown-menu>
          </template>
        </el-dropdown>
      </div>
      <!-- 批量态：顺序固定为 全选/取消全选 → 批量按钮 → 取消（最后），不显示「已选 N 项」。
           「全选」只作用于当前页，与桌面表头复选框一致；放不下时整行横滑（.dd-scroll-row）。 -->
      <div v-else class="dd-scroll-row dd-mobile-batch-bar">
        <el-button :icon="Check" @click="toggleSelectAllOnPage">
          {{ allSelectedOnPage ? "取消全选" : "全选" }}
        </el-button>
        <el-button
          :icon="RefreshRight"
          :disabled="batchReinstallIds.length === 0"
          @click="handleBatchReinstall"
        >
          重装
        </el-button>
        <el-button type="danger" :icon="Delete" @click="handleBatchDelete">
          卸载
        </el-button>
        <el-button :icon="Close" @click="clearSelection">取消</el-button>
      </div>

      <div class="status-tabs deps-type-tabs dd-scroll-row dd-mobile-bleed">
        <button
          v-for="tab in depTypeTabs"
          :key="tab.key"
          :class="['status-tab', { active: activeTab === tab.key }]"
          @click="switchDepType(tab.key)"
        >
          {{ tab.label }}
          <DdBadge
            :value="failedByType[tab.key]"
            level="danger"
            :title="tab.badgeTitle"
          />
        </button>
      </div>

      <div v-if="activeTab === 'python'" class="deps-mobile-python">
        <el-select
          v-model="pythonVersion"
          placeholder="Python 版本"
          @change="handlePythonVersionChange"
        >
          <el-option
            v-for="runtime in pythonRuntimes"
            :key="runtime.version"
            :label="
              runtime.default ? `${runtime.label}（默认）` : runtime.label
            "
            :value="runtime.version"
          >
            <div class="python-runtime-option">
              <span>{{ runtime.label }}</span>
              <el-tag v-if="runtime.default" size="small" type="success"
                >默认</el-tag
              >
              <el-tag v-else-if="runtime.venv_healthy" size="small" type="info"
                >已初始化</el-tag
              >
            </div>
          </el-option>
        </el-select>
        <el-button
          :disabled="pythonVersion === pythonDefaultVersion"
          @click="setCurrentPythonDefault"
        >
          设为默认
        </el-button>
      </div>
    </div>

    <template v-else>
    <div class="deps-tabs dd-rise" style="--dd-i: 2">
      <!-- 类型页签上各挂一个自己的失败数。三者之和 == 侧栏「依赖管理」角标，
           用户看到角标 9 时能直接看出是哪一类、各几个，不用挨个切标签去凑。
           与右边那排状态角标（level="info" + show-zero 的中性计数）刻意不同：
           这三个是「需要用户处理」的告警，所以用 danger，且为 0 时整个消失，
           免得三个红 0 常驻抢注意力。
           页签由 depTypeTabs 数据驱动，与移动端那一排共用同一份定义和 switchDepType()。 -->
      <div class="status-tabs">
        <button
          v-for="tab in depTypeTabs"
          :key="tab.key"
          :class="['status-tab', { active: activeTab === tab.key }]"
          @click="switchDepType(tab.key)"
        >
          {{ tab.label }}
          <DdBadge
            :value="failedByType[tab.key]"
            level="danger"
            :title="tab.badgeTitle"
          />
        </button>
      </div>
      <!-- 状态筛选是【纯前端】的：点这三个按钮只改 statusFilter + 复位页码，不重新请求
           （对比左边的 Node/Python/Linux 组，那一组每次都 loadData()）。
           depsApi.list() 一次就把当前运行时的全量依赖拉回来，分页也是前端 slice，
           所以角标里的数字是真实总数，不是拿当前页凑的。 -->
      <div class="status-tabs status-tabs--filter">
        <button
          :class="['status-tab', { active: statusFilter === '' }]"
          @click="
            statusFilter = '';
            depsPage = 1;
          "
        >
          全部
          <DdBadge
            :value="allCount"
            level="info"
            show-zero
            title="当前列表的依赖总数"
          />
        </button>
        <button
          :class="[
            'status-tab status-tab--success',
            { active: statusFilter === 'installed' },
          ]"
          @click="
            statusFilter = statusFilter === 'installed' ? '' : 'installed';
            depsPage = 1;
          "
        >
          已安装
          <DdBadge
            :value="installedCount"
            level="info"
            show-zero
            title="已安装的依赖数"
          />
        </button>
        <button
          :class="[
            'status-tab status-tab--danger',
            { active: statusFilter === 'failed' },
          ]"
          @click="
            statusFilter = statusFilter === 'failed' ? '' : 'failed';
            depsPage = 1;
          "
        >
          失败
          <DdBadge
            :value="failedCount"
            level="info"
            show-zero
            title="安装失败的依赖数"
          />
        </button>
      </div>
    </div>

    <div class="toolbar dd-rise" style="--dd-i: 3">
      <!-- 按 design-system §4.2：左槽使用 1×1 网格叠放常规操作与批量栏，两支常驻 DOM、靠 visibility 切换，
           勾选前后工具栏宽度与高度恒定不变，消除表格与右侧控件跳动。 -->
      <div class="toolbar__left">
        <div class="toolbar__filters" :class="{ 'is-swapped-out': selectedIds.length > 0 }">
          <!-- 原来这里平铺 5 个按钮（新增依赖 / 刷新 / 批量重装 / 导出清单 / 镜像源设置），
               主次不分、横着吃掉半条工具条。改成 Split Button：
               主体是「新增依赖」——它只打开一个弹窗，是这 5 个里点错代价最小的一个；
               其余 4 项收进菜单（后来又加了系统命令行、安装 Playwright 运行环境，菜单与移动端下拉共用）。
               原按钮的 :loading 在菜单项里没有对应表达，
               降级成 disabled（见 toolbarActionItems），避免刷新/导出进行中被重复点。 -->
          <DdSplitButton
            label="新增依赖"
            :icon="Plus"
            type="primary"
            size="default"
            :items="toolbarActionItems"
            @click="openCreateDialog"
            @command="onToolbarAction"
          />
        </div>
        <div class="batch-actions" :class="{ 'is-swapped-out': selectedIds.length === 0 }">
          <span class="batch-actions__count">已选 {{ selectedIds.length }} 项</span>
          <el-button
            :disabled="batchReinstallIds.length === 0"
            @click="handleBatchReinstall"
          >
            批量重装
          </el-button>
          <el-button type="danger" @click="handleBatchDelete">批量卸载</el-button>
          <el-button @click="clearSelection">取消选择</el-button>
        </div>
      </div>
      <div class="toolbar__right">
        <el-select
          v-if="activeTab === 'python'"
          v-model="pythonVersion"
          class="toolbar__python-version"
          placeholder="Python 版本"
          @change="handlePythonVersionChange"
        >
          <el-option
            v-for="runtime in pythonRuntimes"
            :key="runtime.version"
            :label="
              runtime.default ? `${runtime.label}（默认）` : runtime.label
            "
            :value="runtime.version"
          >
            <div class="python-runtime-option">
              <span>{{ runtime.label }}</span>
              <el-tag v-if="runtime.default" size="small" type="success"
                >默认</el-tag
              >
              <el-tag v-else-if="runtime.venv_healthy" size="small" type="info"
                >已初始化</el-tag
              >
            </div>
          </el-option>
        </el-select>
        <el-button
          v-if="activeTab === 'python'"
          @click="setCurrentPythonDefault"
          :disabled="pythonVersion === pythonDefaultVersion"
        >
          设为默认
        </el-button>
        <el-input
          v-model="searchKeyword"
          placeholder="搜索依赖包名称..."
          clearable
          class="toolbar__search"
          @keyup.enter="depsPage = 1"
          @clear="depsPage = 1"
        >
          <template #prefix
            ><el-icon><Search /></el-icon
          ></template>
        </el-input>
        <el-select
          v-model="statusFilter"
          placeholder="所有状态"
          clearable
          class="toolbar__filter"
          @change="depsPage = 1"
        >
          <el-option label="已安装" value="installed" />
          <el-option label="安装中" value="installing" />
          <el-option label="排队中" value="queued" />
          <el-option label="失败" value="failed" />
          <el-option label="已取消" value="cancelled" />
          <el-option label="卸载中" value="removing" />
        </el-select>
      </div>
    </div>
    </template>

    <el-alert
      v-if="activeTab === 'python'"
      class="python-runtime-hint"
      type="info"
      :closable="false"
      show-icon
    >
      <template #title>Python 多版本说明</template>
      <div class="python-runtime-hint__body">
        二进制部署不会内置三个
        Python，只需要在服务器安装实际要用的版本；面板会为可用版本创建独立依赖环境，未安装版本会明确提示不可用，不影响其他版本运行。
      </div>
      <div class="python-runtime-hint__body">
        当前正在展示 <b>Python {{ pythonVersion }}</b> 的依赖列表；
        系统默认版本是 <b>Python {{ pythonDefaultVersion }}</b>。
        如果默认版本当前不可用，页面会自动切到第一个可用版本，避免打开就是空白或报错。
      </div>
      <div class="python-runtime-hint__status">
        <el-tag
          v-for="runtime in pythonRuntimes"
          :key="runtime.version"
          size="small"
          :type="runtime.available ? 'success' : 'warning'"
          effect="plain"
        >
          {{ runtime.label }}：{{ runtime.available ? "可用" : "需先安装" }}
        </el-tag>
      </div>
    </el-alert>

    <!-- Linux 页签的说明。以前这里什么都没有，页签上只写着「Linux」两个字，
         用户根本看不出「这里装的就是 apk / apt 系统包」——issue #120 的用户
         在 Alpine 下装不上 opencv-python，问的是「能不能加个装系统依赖的功能」，
         而这个功能一直都在。包管理器/发行版/镜像源可配性全来自 GET /deps/mirrors，
         挂载时就拉好了（见 loadMirrorMeta），不用等用户打开镜像源弹窗。 -->
    <el-alert
      v-if="activeTab === 'linux'"
      class="linux-runtime-hint"
      type="info"
      :closable="false"
      show-icon
    >
      <template #title>Linux 系统包说明</template>
      <div class="linux-runtime-hint__body">
        这里安装的是<b>操作系统软件包</b>（走
        {{ linuxMirrorManagerText }} 包管理器），不是 Python / Node.js
        包。pip 安装时报缺 gcc、缺头文件、需要现场编译的依赖，要先在这里补上。
      </div>
      <div class="linux-runtime-hint__body">
        当前检测：包管理器 <b>{{ linuxMirrorManagerText }}</b>
        <span v-if="linuxMirrorDistributionText">
          · 发行版 <b>{{ linuxMirrorDistributionText }}</b></span
        >
        · 镜像源{{ linuxMirrorSupported ? "可配置" : "不可配置" }}。
        <span v-if="linuxMirrorMessage">{{ linuxMirrorMessage }}</span>
      </div>
      <div class="linux-runtime-hint__actions">
        <el-button
          type="primary"
          size="small"
          :disabled="linuxToolchainPackages.length === 0"
          @click="openLinuxToolchainInstall"
        >
          安装编译工具链
        </el-button>
        <span class="linux-runtime-hint__tip">{{ linuxToolchainTip }}</span>
      </div>
      <!-- Playwright 一键安装（v3.3.1，issue #142）：单独一行，按钮与它自己的说明并排，
           不和上面工具链那行挤在一起（两段说明挨着会分不清是哪个按钮的）。
           不支持 / 状态拉取失败时按钮置灰，原因直接写在旁边：disabled 的按钮不会触发 tooltip。 -->
      <div class="linux-runtime-hint__actions">
        <el-button
          type="primary"
          size="small"
          :disabled="!playwrightSupported"
          :loading="playwrightInstalling"
          @click="handlePlaywrightInstall"
        >
          安装 Playwright 运行环境
        </el-button>
        <span class="linux-runtime-hint__tip">
          <span v-if="playwrightReady" class="linux-runtime-hint__ready"
            >已就绪 ·</span
          >
          {{ playwrightTip }}
        </span>
      </div>
    </el-alert>

    <div v-if="isMobile" class="dd-mobile-list dd-rise" style="--dd-i: 4">
      <!-- 移动卡片（v3.3.1，issue #143）：首行 复选框 → 名称 → 状态标签（紧跟名称）→ 右上角「···」；
           字段区标签与值横排；末行右侧只留最常用的「日志」「卸载」，
           取消 / 重装 / 强制卸载收进「···」（与桌面操作列同一份 depActionItems，见 depCardMenuItems）。 -->
      <div
        v-for="row in paginatedDepsList"
        :key="row.id"
        class="dd-mobile-card"
        :class="{ 'is-locate-highlight': row.id === locateHighlightId }"
      >
        <div class="dd-mobile-card__head">
          <el-checkbox
            :model-value="isSelected(row.id)"
            :aria-label="`选择 ${row.name}`"
            @change="toggleSelected(row.id, $event)"
          />
          <span class="dd-mobile-card__name" :title="row.name">{{
            row.name
          }}</span>
          <!-- 与桌面表格同一套过渡：key 绑状态值，只做 opacity -->
          <Transition name="dd-status-switch" mode="out-in">
            <el-tag
              :key="row.status"
              :type="statusType(row.status)"
              size="small"
              effect="light"
              >{{ statusLabel(row.status) }}</el-tag
            >
          </Transition>
          <DdMoreMenu
            :items="depCardMenuItems(row)"
            @command="(key: string) => onDepAction(key, row)"
          />
        </div>
        <div class="dd-mobile-card__rows">
          <div class="dd-mobile-card__row">
            <span class="dd-mobile-card__row-label">创建时间</span>
            <span class="dd-mobile-card__row-value">{{
              formatDateTime(row.created_at)
            }}</span>
          </div>
          <div v-if="activeTab === 'python'" class="dd-mobile-card__row">
            <span class="dd-mobile-card__row-label">Python 版本</span>
            <span class="dd-mobile-card__row-value">{{
              row.python_version || pythonDefaultVersion
            }}</span>
          </div>
        </div>
        <div class="dd-mobile-card__footer">
          <div class="dd-mobile-card__footer-actions">
            <el-button type="primary" plain @click="viewLog(row)">日志</el-button>
            <el-button
              type="danger"
              plain
              :disabled="isProcessing(row.status)"
              @click="handleDelete(row)"
            >
              卸载
            </el-button>
          </div>
        </div>
      </div>

      <el-empty
        v-if="!loading && paginatedDepsList.length === 0"
        description="暂无依赖"
      />
    </div>

    <div v-else class="table-card dd-spot dd-rise" style="--dd-i: 4">
      <el-table
        ref="depsTableRef"
        :data="paginatedDepsList"
        v-loading="loading"
        style="width: 100%"
        :row-class-name="({ row }: { row: any }) => (row.id === locateHighlightId ? 'is-locate-highlight' : '')"
        @selection-change="handleSelectionChange"
        :header-cell-style="{
          background: 'var(--el-fill-color-light)',
          color: 'var(--el-text-color-regular)',
          fontWeight: 600,
          fontSize: '13px',
        }"
      >
        <el-table-column type="selection" width="40" />
        <el-table-column prop="name" label="名称" min-width="160">
          <template #default="{ row }">
            <div class="dep-name-cell">
              <span
                class="dep-name-avatar"
                :style="{ '--dd-avatar': getLetterColor(row.name) }"
                >{{ (row.name || "?").charAt(0).toUpperCase() }}</span
              >
              <span class="dep-name-text" :title="row.name">{{
                row.name
              }}</span>
            </div>
          </template>
        </el-table-column>
        <!-- 这里原本有一列「版本」，读 row.version，已整列删除。
             原因：Dependency 模型（server/model/dependency.go）根本没有 version 字段，
             ToDict() 也不输出它，所以 Node.js / Python / Linux 三种类型下这一列
             恒定渲染「-」，是一列纯噪音。
             真要展示版本，得先让后端在安装成功后把实际装到的版本回写进模型
             （pip show / npm ls / apk info 的输出解析），那是另一件事，不在本次范围。 -->
        <el-table-column
          v-if="activeTab === 'python'"
          prop="python_version"
          label="Python"
          width="110"
        >
          <template #default="{ row }">
            <el-tag size="small" type="info">{{
              row.python_version || pythonDefaultVersion
            }}</el-tag>
          </template>
        </el-table-column>
        <!-- 依赖状态是全站流转最密集的一处：排队中 → 安装中 → 已安装/失败，
             卸载时还会走 卸载中 → 行消失，全靠 3s 轮询推进。硬切换时用户只会看到
             文字突然变了，分不清是自己看漏了还是真的变了。out-in 让旧标签先淡出、
             新标签再淡入，交接过程本身就是「状态更新了」的信号。
             key 必须绑 row.status（状态值）：绑 row.id 的话同一行的 key 永远不变，
             过渡一次也不会触发。
             只做 opacity，不做位移——表格行里的位移会带着整行一起晃。 -->
        <el-table-column label="状态" width="100" align="center">
          <template #default="{ row }">
            <Transition name="dd-status-switch" mode="out-in">
              <el-tag
                :key="row.status"
                :type="statusType(row.status)"
                size="small"
                effect="light"
                round
                >{{ statusLabel(row.status) }}</el-tag
              >
            </Transition>
          </template>
        </el-table-column>
        <el-table-column prop="created_at" label="创建时间" width="180">
          <template #default="{ row }">
            <span class="time-text">{{ formatDateTime(row.created_at) }}</span>
          </template>
        </el-table-column>
        <!-- 原来是「详情 + 取消/重装 + 更多▾」的手工版 split button：三个 text 按钮
             靠 .action-btns 的 gap 拼出来，还得把按钮压到 26px 才塞得进 176px。
             换成真正的 DdSplitButton 后只剩一个按钮组。
             宽度是【浏览器实测】的，不是估的（估算口诀在 caret 那一项上会算小）：
               主体「详情」42px（2×12 文字 + 本页 .action-btns 覆盖的 8px×2 内边距 + 2px 边框）
               caret     32px  ← 不是 24px。EP 的 `.el-dropdown--small .el-dropdown__caret-button
                                { width: 24px }` 命中不了：el-dropdown 根节点只挂
                                `el-dropdown` + `is-disabled`，不带 size 修饰类，
                                所以 size="small" 的 caret 实际吃的是基础档 32px。
               合计 42 + 32 − 1（button-group 的 -1px 负边距）= 73px
             .el-table .cell 是 padding:0 12px，可用内容宽 = 列宽 − 24。
             列宽 176 → 110（可用 86px，余量 13px，与环境变量页同口径）。
             最初改成 100 时余量只剩 3px，够是够，但把「详情」改成任何 3 字标签就会溢出，
             而 .cell 一溢出就会变成可滚动容器、点按钮时整行左移且不复位。 -->
        <el-table-column label="操作" width="110" fixed="right" align="center">
          <template #default="{ row }">
            <div class="action-btns">
              <DdSplitButton
                label="详情"
                type="primary"
                size="small"
                :items="depActionItems(row)"
                @click="viewLog(row)"
                @command="(key: string) => onDepAction(key, row)"
              />
            </div>
          </template>
        </el-table-column>
      </el-table>
    </div>

    <div class="pagination-bar dd-rise" style="--dd-i: 5">
      <span class="pagination-total">共 {{ depsTotal }} 条数据</span>
      <el-pagination
        v-model:current-page="depsPage"
        v-model:page-size="depsPageSize"
        :total="depsTotal"
        :page-sizes="[10, 20, 50, 100]"
        :layout="isMobile ? 'prev, pager, next' : 'sizes, prev, pager, next, jumper'"
        @current-change="handlePageChange"
        @size-change="handlePageSizeChange"
      />
    </div>
    <el-dialog
      v-model="showCreateDialog"
      title="新建依赖"
      width="500px"
      :fullscreen="dialogFullscreen"
    >
      <el-form
        :label-width="isMobile ? 'auto' : '80px'"
        :label-position="isMobile ? 'top' : 'right'"
      >
        <el-form-item label="类型">
          <el-radio-group v-model="createType">
            <el-radio value="nodejs">Node.js</el-radio>
            <el-radio value="python">Python3</el-radio>
            <el-radio value="linux">Linux</el-radio>
          </el-radio-group>
        </el-form-item>
        <el-form-item v-if="createType === 'python'" label="版本">
          <el-alert
            :title="`会同步安装到当前镜像支持的 ${pythonRuntimeInstallSummary}；单版本镜像只会安装到当前小版本`"
            type="info"
            :closable="false"
            show-icon
          />
        </el-form-item>
        <!-- 系统包必须 root 才装得上。非 root 部署下不提前说，用户只能靠
             「先提交、再失败、再点详情、再读日志」才知道本来就装不了。 -->
        <el-form-item v-if="createType === 'linux'" label="说明">
          <el-alert
            :title="linuxCreateHint"
            type="warning"
            :closable="false"
            show-icon
          />
        </el-form-item>
        <el-form-item label="名称">
          <el-input
            v-model="createNames"
            type="textarea"
            :rows="5"
            :placeholder="createNamePlaceholder"
          />
        </el-form-item>
        <el-form-item label="自动拆分">
          <div class="auto-split-row">
            <el-switch v-model="autoSplit" />
            <span class="auto-split-row__hint">开启后自动按换行、空格、逗号拆分为多个依赖</span>
          </div>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="showCreateDialog = false">取消</el-button>
        <el-button type="primary" @click="handleCreate" :loading="creating"
          >安装</el-button
        >
      </template>
    </el-dialog>
    <el-dialog
      v-model="showLogDialog"
      title="安装日志"
      width="70%"
      :fullscreen="dialogFullscreen"
      class="dep-log-dialog"
    >
      <div class="log-dialog-toolbar">
        <div class="log-dialog-status">
          <!-- 这个标签必须读依赖行的真实 status，不能读「日志流是否结束」。
               以前用 logDone 判定，导致日志流断开（例如长时间无输出）时明明还在装，
               却显示成绿色的「已完成」。 -->
          <!-- 弹窗开着的时候状态是由 SSE / 轮询实时推进的，这里是用户盯得最紧的一处，
               同样包 out-in。key 绑的是状态值而不是分支：
                 - 前两个分支之间（安装中 → 失败）跨分支切，会触发；
                 - 分支内部（排队中 → 安装中，两者都算 isProcessing）状态值变了也会触发，
                   靠分支切换的隐式 key 反而漏掉这一档。
               「已卸载」分支没有对应的 status（行已被后端删掉），单独给个静态 key。 -->
          <Transition name="dd-status-switch" mode="out-in">
            <el-tag v-if="logRowRemoved" key="removed" type="success" size="small"
              >已卸载</el-tag
            >
            <el-tag
              v-else-if="currentLogRow && isProcessing(currentLogRow.status)"
              :key="currentLogRow.status"
              type="warning"
              size="small"
              class="running-tag"
            >
              <LoadingMotion
                variant="dots"
                size="sm"
                tone="warning"
                :stacked="false"
              />
              <span>{{ statusLabel(currentLogRow.status) }}</span>
            </el-tag>
            <el-tag
              v-else-if="currentLogRow"
              :key="currentLogRow.status"
              :type="statusType(currentLogRow.status)"
              size="small"
              >{{ statusLabel(currentLogRow.status) }}</el-tag
            >
          </Transition>
          <el-tag v-if="logStreamNotice" type="info" size="small">{{
            logStreamNotice
          }}</el-tag>
        </div>
        <!-- 只有 installing / removing 能取消：服务端的 Cancel 接口对 queued 直接返回 400
             「当前依赖任务未在处理中」，把按钮显示出来只会让用户点了报错。 -->
        <el-button
          v-if="
            currentLogRow &&
            !logRowRemoved &&
            (currentLogRow.status === 'installing' ||
              currentLogRow.status === 'removing')
          "
          type="warning"
          plain
          size="small"
          @click="handleCancel(currentLogRow)"
        >
          取消当前任务
        </el-button>
      </div>
      <pre
        ref="logContainerRef"
        class="log-content dd-log-surface"
        v-html="logContentHtml"
      ></pre>
      <!-- 移动端（全屏）把关闭入口放到右下角：有 footer 时右上角 × 由 global.scss 统一隐藏。
           只在全屏时渲染，桌面仍是原来那样没有底栏。置 false 照样走 watch(showLogDialog) 里关 SSE 等收尾。 -->
      <template v-if="dialogFullscreen" #footer>
        <el-button @click="showLogDialog = false">关闭</el-button>
      </template>
    </el-dialog>
    <el-dialog
      v-model="showMirrorDialog"
      title="软件包镜像源设置"
      width="560px"
      :fullscreen="dialogFullscreen"
    >
      <el-form
        :label-width="isMobile ? 'auto' : '110px'"
        :label-position="isMobile ? 'top' : 'right'"
        v-loading="mirrorLoading"
      >
        <el-form-item label="Python (pip)">
          <el-input
            v-model="mirrorForm.pip_mirror"
            placeholder="留空恢复默认加速源"
            clearable
          >
            <template #append>
              <el-dropdown
                @command="(v: string) => (mirrorForm.pip_mirror = v)"
                trigger="click"
              >
                <el-button>快捷选择</el-button>
                <template #dropdown>
                  <el-dropdown-menu>
                    <!-- issue #146 / v3.3.2：阿里云 pip 限速到 100KB/s 以下，默认源改成腾讯云。
                         阿里云不删、只摘掉「(默认)」—— 阿里云 ECS 内网走它反而最快，
                         直接删会让这批用户的现有选择在下拉里凭空消失。 -->
                    <el-dropdown-item
                      command="https://mirrors.aliyun.com/pypi/simple"
                      >阿里云</el-dropdown-item
                    >
                    <el-dropdown-item
                      command="https://pypi.tuna.tsinghua.edu.cn/simple"
                      >清华大学</el-dropdown-item
                    >
                    <el-dropdown-item command="https://pypi.doubanio.com/simple"
                      >豆瓣</el-dropdown-item
                    >
                    <el-dropdown-item
                      command="https://mirrors.cloud.tencent.com/pypi/simple"
                      >腾讯云 (默认)</el-dropdown-item
                    >
                    <el-dropdown-item
                      command="https://repo.huaweicloud.com/repository/pypi/simple"
                      >华为云</el-dropdown-item
                    >
                    <el-dropdown-item
                      command="https://mirrors.ctyun.cn/pypi/simple"
                      >天翼云</el-dropdown-item
                    >
                    <!-- issue #165 / v3.3.9：后端不再把官方源换成默认加速源，这里给个直达选项（海外机器直连更快）。 -->
                    <el-dropdown-item command="https://pypi.org/simple"
                      >PyPI 官方</el-dropdown-item
                    >
                    <el-dropdown-item command=""
                      >恢复默认加速源</el-dropdown-item
                    >
                  </el-dropdown-menu>
                </template>
              </el-dropdown>
            </template>
          </el-input>
        </el-form-item>
        <el-form-item label="Node.js (npm)">
          <el-input
            v-model="mirrorForm.npm_mirror"
            placeholder="留空恢复默认加速源"
            clearable
          >
            <template #append>
              <el-dropdown
                @command="(v: string) => (mirrorForm.npm_mirror = v)"
                trigger="click"
              >
                <el-button>快捷选择</el-button>
                <template #dropdown>
                  <el-dropdown-menu>
                    <el-dropdown-item command="https://registry.npmmirror.com"
                      >淘宝 (npmmirror)</el-dropdown-item
                    >
                    <el-dropdown-item
                      command="https://mirrors.cloud.tencent.com/npm/"
                      >腾讯云</el-dropdown-item
                    >
                    <el-dropdown-item
                      command="https://repo.huaweicloud.com/repository/npm/"
                      >华为云</el-dropdown-item
                    >
                    <!-- issue #165 同一个根因：npm 官方源现在也原样保存、原样生效。 -->
                    <el-dropdown-item command="https://registry.npmjs.org/"
                      >npm 官方</el-dropdown-item
                    >
                    <el-dropdown-item command=""
                      >恢复默认加速源</el-dropdown-item
                    >
                  </el-dropdown-menu>
                </template>
              </el-dropdown>
            </template>
          </el-input>
        </el-form-item>
        <el-form-item :label="linuxMirrorLabel">
          <el-input
            v-model="mirrorForm.linux_mirror"
            :placeholder="
              linuxMirrorSupported
                ? '留空恢复默认加速源'
                : '当前包管理器暂不支持镜像设置'
            "
            :disabled="!linuxMirrorSupported"
            clearable
          >
            <template #append>
              <el-dropdown
                @command="(v: string) => (mirrorForm.linux_mirror = v)"
                trigger="click"
                :disabled="
                  !linuxMirrorSupported || linuxMirrorOptions.length === 0
                "
              >
                <el-button
                  :disabled="
                    !linuxMirrorSupported || linuxMirrorOptions.length === 0
                  "
                  >快捷选择</el-button
                >
                <template #dropdown>
                  <el-dropdown-menu>
                    <el-dropdown-item
                      v-for="option in linuxMirrorOptions"
                      :key="option.value"
                      :command="option.value"
                    >
                      {{ option.label }}
                    </el-dropdown-item>
                    <el-dropdown-item command=""
                      >恢复默认加速源</el-dropdown-item
                    >
                  </el-dropdown-menu>
                </template>
              </el-dropdown>
            </template>
          </el-input>
          <div class="mirror-hint">
            当前检测：{{ linuxMirrorManagerText }}
            <span v-if="linuxMirrorDistributionText">
              / {{ linuxMirrorDistributionText }}</span
            >
            <span v-if="linuxMirrorMessage">。{{ linuxMirrorMessage }}</span>
          </div>
        </el-form-item>
        <el-alert type="info" :closable="false" show-icon>
          依赖管理默认优先使用加速源；清空输入框并保存，会恢复到内置的默认加速源配置。
        </el-alert>
      </el-form>
      <template #footer>
        <el-button @click="showMirrorDialog = false">取消</el-button>
        <el-button
          type="primary"
          @click="handleSaveMirrors"
          :loading="mirrorSaving"
          >保存</el-button
        >
      </template>
    </el-dialog>

    <!-- 系统命令行。入口放在依赖页是因为 issue #120 的用户正是在「装不上依赖」
         这个场景下提的需求；接口是 admin-only，所以整块按角色 gate 掉。 -->
    <SystemConsoleDialog
      v-if="isAdmin"
      v-model:visible="showConsoleDialog"
      :is-mobile="isMobile"
    />
  </div>
</template>

<script setup lang="ts">
import {
  ref,
  onMounted,
  onBeforeUnmount,
  onActivated,
  watch,
  computed,
  nextTick,
  h,
} from "vue";
import {
  depsApi,
  type DepsFailedByType,
  type MirrorsResponse,
  type PlaywrightStatus,
  type PythonRuntimeInfo,
} from "@/api/deps";
import {
  androidRuntimeApi,
  type AndroidRuntimeStatus,
  type AndroidRuntimePreset,
} from "@/api/androidRuntime";
import { ElMessage, ElMessageBox } from "element-plus";
import { useRoute, useRouter } from "vue-router";
import {
  ArrowDown,
  Box,
  Check,
  ChromeFilled,
  Close,
  Cpu,
  Delete,
  Download,
  Monitor,
  Plus,
  Refresh,
  RefreshRight,
  Search,
  Setting,
} from "@element-plus/icons-vue";
import DdSplitButton from "@/components/ui/DdSplitButton.vue";
import type { SplitButtonItem } from "@/components/ui/DdSplitButton.vue";
import DdMoreMenu from "@/components/ui/DdMoreMenu.vue";
import DdBadge from "@/components/ui/DdBadge.vue";
import SystemConsoleDialog from "./components/SystemConsoleDialog.vue";
import {
  openAuthorizedEventStream,
  type EventStreamConnection,
} from "@/utils/sse";
import { usePageActivity } from "@/composables/usePageActivity";
import { useResponsive } from "@/composables/useResponsive";
import { useLogAutoFollow } from "@/composables/useLogAutoFollow";
import { useAuthStore } from "@/stores/auth";
import { useBadgesStore } from "@/stores/badges";
import { canAdminister } from "@/utils/roles";
import { ansiToHtml, normalizeAnsi } from "@/utils/ansi";
import { formatDateTime } from "@/utils/datetime";
import { scrollListToTop } from "@/utils/scrollToTop";
import { motionLevel } from "@/utils/motion";

const badgesStore = useBadgesStore();
const authStore = useAuthStore();
// 系统命令行那组接口是 JWTAuth + RequireAdmin 的管理员接口，非管理员点进去必然 403，
// 所以入口按角色隐藏（与订阅页里同一套判断）。
const isAdmin = computed(() => canAdminister(authStore.user?.role));
const showConsoleDialog = ref(false);

// ---------- Android 面具版脚本运行时 ----------
const androidStatus = ref<AndroidRuntimeStatus | null>(null);
const androidInstallingName = ref<string>("");
const androidInstallLog = ref<string[]>([]);
const androidInstallLogHtml = computed(() =>
  ansiToHtml(normalizeAnsi(androidInstallLog.value.join("\n"))),
);
// Android 运行时安装日志的自动跟随：安装中上翻即暂停，滚回底部恢复
const androidLogRef = ref<HTMLElement>();
const androidFollow = useLogAutoFollow(androidLogRef);
let androidInstallAbort: AbortController | null = null;

async function loadAndroidStatus() {
  try {
    const res = await androidRuntimeApi.status();
    androidStatus.value = res.data;
  } catch (e) {
    androidStatus.value = null;
  }
}

function presetFor(name: string): AndroidRuntimePreset | undefined {
  return androidStatus.value?.presets?.find((p) => p.name === name);
}

async function installAndroidRuntime(name: string) {
  if (androidInstallingName.value) return;
  const preset = presetFor(name);
  if (!preset) {
    ElMessage.warning("当前架构没有预置下载源");
    return;
  }
  try {
    await ElMessageBox.confirm(
      `将从 ${preset.url} 下载约 ${preset.size_mb}MB 并解压到 /data/adb/daidai-panel/bin/${name}，是否继续？`,
      "安装确认",
      { confirmButtonText: "开始安装", cancelButtonText: "取消" },
    );
  } catch {
    return;
  }

  androidInstallingName.value = name;
  androidInstallLog.value = [
    `[${new Date().toLocaleTimeString()}] 准备安装 ${name}...`,
  ];
  androidInstallAbort = new AbortController();

  try {
    const resp = await androidRuntimeApi.installStream(
      name,
      androidInstallAbort.signal,
    );
    if (!resp.ok) {
      const text = await resp.text();
      androidInstallLog.value.push(`HTTP ${resp.status}: ${text}`);
      ElMessage.error("安装失败: HTTP " + resp.status);
      return;
    }
    const reader = resp.body?.getReader();
    if (!reader) {
      ElMessage.error("无法建立流式连接");
      return;
    }
    const decoder = new TextDecoder();
    let buf = "";
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      buf += decoder.decode(value, { stream: true });
      let idx;
      while ((idx = buf.indexOf("\n\n")) >= 0) {
        const line = buf.slice(0, idx);
        buf = buf.slice(idx + 2);
        const m = line.match(/^data:\s?(.*)$/);
        if (m && m[1] !== undefined)
          androidInstallLog.value.push(m[1].replace(/\\n/g, "\n"));
      }
    }
    ElMessage.success(`${name} 安装完成`);
    await loadAndroidStatus();
  } catch (e: any) {
    if (e?.name !== "AbortError") {
      androidInstallLog.value.push("异常: " + (e?.message || String(e)));
      ElMessage.error(e?.message || "安装过程异常");
    }
  } finally {
    androidInstallingName.value = "";
    androidInstallAbort = null;
  }
}

async function uninstallAndroidRuntime(name: string) {
  try {
    await ElMessageBox.confirm(
      `确定移除 /data/adb/daidai-panel/bin/${name}？`,
      "确认",
      { type: "warning" },
    );
  } catch {
    return;
  }
  try {
    await androidRuntimeApi.uninstall(name);
    ElMessage.success("已移除");
    await loadAndroidStatus();
  } catch (e: any) {
    ElMessage.error("移除失败: " + (e?.message || String(e)));
  }
}
// ---------- /Android 面具版 ----------

const activeTab = ref("nodejs");

/** 三种依赖类型，键与 failed_by_type 的三个键一一对应 */
type DepType = keyof DepsFailedByType;
/**
 * 类型页签（Node.js / Python3 / Linux）的定义，桌面 .deps-tabs 与移动端第二行共用。
 * 以前三个按钮各内联一遍「切页签 + 复位页码 + 拉数据」，两端各抄一份就是六处，抽成数据驱动。
 */
const depTypeTabs: ReadonlyArray<{ key: DepType; label: string; badgeTitle: string }> = [
  { key: "nodejs", label: "Node.js", badgeTitle: "Node.js 下安装失败的依赖数" },
  {
    key: "python",
    label: "Python3",
    badgeTitle: "Python 下安装失败的依赖数（含所有 Python 版本）",
  },
  { key: "linux", label: "Linux", badgeTitle: "Linux 下安装失败的依赖数" },
];

function switchDepType(type: DepType) {
  activeTab.value = type;
  depsPage.value = 1;
  void loadData();
  // Linux 页签的说明块里有 Playwright 一键安装按钮，切过来时刷新一次它的状态（装完没有、支不支持）
  if (type === "linux") void loadPlaywrightStatus();
}

const pythonRuntimes = ref<PythonRuntimeInfo[]>([]);
const pythonDefaultVersion = ref("3.12");
const pythonVersion = ref("3.12");
const createPythonVersion = ref("3.12");
const pythonRuntimeInstallSummary = computed(() => {
  const labels = pythonRuntimes.value.map((item) => item.label || `Python ${item.version}`);
  return labels.length > 0 ? labels.join(" / ") : "Python 3.12";
});
const depsList = ref<any[]>([]);
/**
 * Node.js / Python3 / Linux 三个类型页签上各自的失败数，由 GET /deps 一并带回。
 *
 * 【为什么不在前端自己数】
 * depsList 只有【当前选中类型】（python 还只有当前版本）的依赖，数出来的失败数
 * 永远只是三分之一，与侧栏那个跨类型汇总的角标对不上——这正是用户看到「侧栏 9、
 * 页面失败 2」的原因。服务端那份是全量统计，且本页本来就在轮询这个接口，
 * 计数天然新鲜、零额外请求。
 */
const failedByType = ref<DepsFailedByType>({ nodejs: 0, python: 0, linux: 0 });
const loading = ref(false);
const showCreateDialog = ref(false);
const showLogDialog = ref(false);
const logContent = ref("");
const logContentHtml = computed(() =>
  ansiToHtml(normalizeAnsi(logContent.value || "暂无日志")),
);
// logDone 的语义是「日志流已结束」，不等于「任务已完成」。
// 任务是否完成一律看 currentLogRow.status。
const logDone = ref(true);
// 日志流意外断开（服务端硬超时、网络中断）时给用户的提示，避免用户以为任务停了。
const logStreamNotice = ref("");
// 依赖已被后端删除（卸载成功）。此时 currentLogRow 只是个查不到对应行的旧快照。
const logRowRemoved = ref(false);
const currentLogRow = ref<any | null>(null);
let eventSource: EventStreamConnection | null = null;
const logContainerRef = ref<HTMLElement>();
// 依赖安装日志弹窗的自动跟随：安装中上翻即暂停、滚回底部恢复；已结束记录停在顶部不跟随。
// 弹窗不带 destroy-on-close、容器常驻，所以每次 viewLog 都要 begin/end 重置状态。
const installFollow = useLogAutoFollow(logContainerRef);
let depsLogBuffer: string[] = [];
let depsLogFlushRaf = 0;
const createType = ref("nodejs");
const createNames = ref("");
const autoSplit = ref(true);
const creating = ref(false);
const exporting = ref(false);
const selectedIds = ref<number[]>([]);
const selectedIdSet = computed(() => new Set(selectedIds.value));
const selectedRows = computed(() =>
  depsList.value.filter((dep) => selectedIdSet.value.has(dep.id)),
);
const batchReinstallRows = computed(() =>
  selectedRows.value.filter((dep) => !isProcessing(dep.status)),
);
const batchReinstallIds = computed(() =>
  batchReinstallRows.value.map((dep) => dep.id),
);

/**
 * 工具栏菜单项：桌面 Split Button 与移动端第一行的下拉菜单共用这一份。
 *
 * 主体是「新增依赖」（写在模板上），它只打开弹窗，点错了不产生任何副作用。
 * 分隔线以下是两个会真的装东西的写操作：「安装 Playwright 运行环境」（先弹确认）与「批量重装」，
 * 与上面四个只读/设置类操作隔开；两者都不是不可撤销操作，所以不标 danger。
 * 「批量重装」在移动端隐藏：移动端一勾选，第一行就整行换成批量栏，这一项永远点不到，已挪进批量栏。
 * 必须是 computed：刷新/导出的进行中状态、Playwright 是否支持、「有没有可重装的选中项」都会变。
 */
const toolbarActionItems = computed<SplitButtonItem[]>(() => [
  { key: "refresh", label: "刷新", icon: Refresh, disabled: loading.value },
  {
    key: "export",
    label: "导出清单",
    icon: Download,
    disabled: exporting.value,
  },
  { key: "mirror", label: "镜像源设置", icon: Setting },
  // 系统命令行只对管理员显示：接口本身是 admin-only，给 operator 显示出来只会点出 403
  {
    key: "console",
    label: "系统命令行",
    icon: Monitor,
    visible: isAdmin.value,
  },
  // 与 Linux 页签说明块里的按钮是同一个入口；不支持的原因只写在那边（菜单项置灰后无处显示说明）
  {
    key: "playwright",
    label: "安装 Playwright 运行环境",
    icon: ChromeFilled,
    divided: true,
    disabled: !playwrightSupported.value || playwrightInstalling.value,
  },
  {
    key: "batch-reinstall",
    label: "批量重装",
    icon: RefreshRight,
    disabled: batchReinstallIds.value.length === 0,
    visible: !isMobile.value,
  },
]);

/** 移动端下拉菜单只渲染可见项（DdSplitButton 内部也是这样过滤的） */
const mobileToolbarMenuItems = computed(() =>
  toolbarActionItems.value.filter((item) => item.visible !== false),
);

function onToolbarAction(key: string) {
  if (key === "refresh") {
    loadData();
    // 「刷新」时顺带刷新 Linux 页签上的 Playwright 状态：装完没有，用户多半就是点这里来确认的
    if (activeTab.value === "linux") void loadPlaywrightStatus();
  } else if (key === "export") handleExport();
  else if (key === "mirror") openMirrorDialog();
  else if (key === "console") showConsoleDialog.value = true;
  else if (key === "playwright") void handlePlaywrightInstall();
  else if (key === "batch-reinstall") handleBatchReinstall();
}

/**
 * 操作列 Split Button 的菜单项，按行状态生成。
 *
 * 主体固定是「详情」——只打开日志弹窗，六种状态下都可用、点错零代价。
 * 「取消 / 重装」互斥：只有 installing / removing 能取消（服务端对 queued 的
 * Cancel 直接返回 400），所以用 visible 联动，绝不会出现「安装中的行菜单里
 * 同时挂着取消和重装」。queued 行仍然显示重装但禁用，与改造前一致。
 * 「卸载 / 强制卸载」不可撤销，只能待在菜单里并标红；divided 只加在危险组的
 * 第一项，用一条分隔线把这两项整体隔开——两项都加会在它们中间再画一条线，
 * 反而把同一组危险操作拆散。
 */
function depActionItems(row: any): SplitButtonItem[] {
  const cancellable = row.status === "installing" || row.status === "removing";
  const processing = isProcessing(row.status);
  return [
    { key: "cancel", label: "取消", visible: cancellable },
    {
      key: "reinstall",
      label: "重装",
      visible: !cancellable,
      disabled: processing,
    },
    {
      key: "delete",
      label: "卸载",
      danger: true,
      divided: true,
      disabled: processing,
    },
    {
      key: "force-delete",
      label: "强制卸载",
      danger: true,
      disabled: processing,
    },
  ];
}

function onDepAction(key: string, row: any) {
  if (key === "cancel") handleCancel(row);
  else if (key === "reinstall") handleReinstall(row);
  else if (key === "delete") handleDelete(row);
  else if (key === "force-delete") handleForceDelete(row);
}

/**
 * 移动卡片右上角「···」的菜单项：桌面那份 depActionItems 去掉「卸载」（卡片末行已有实体按钮）。
 * 原来的分隔线挂在「卸载」上，去掉后要改挂到「强制卸载」，否则危险项与取消/重装之间的分隔线就丢了。
 */
function depCardMenuItems(row: any): SplitButtonItem[] {
  return depActionItems(row)
    .filter((item) => item.key !== "delete")
    .map((item) =>
      item.key === "force-delete" ? { ...item, divided: true } : item,
    );
}

let refreshTimer: ReturnType<typeof setInterval> | null = null;
const { isMobile, dialogFullscreen } = useResponsive();
const { isPageActive, isViewActive } = usePageActivity();

const showMirrorDialog = ref(false);
const mirrorLoading = ref(false);
const mirrorSaving = ref(false);
const mirrorForm = ref({ pip_mirror: "", npm_mirror: "", linux_mirror: "" });
// 打开弹窗那一刻的表单原始值快照。保存时逐字段与它比对，只把真改过的字段放进 payload。
// issue #146：以前三个字段恒被提交，「只改 pip、Linux 栏不动」必然报 400（详见 handleSaveMirrors）。
const mirrorFormSnapshot = ref({
  pip_mirror: "",
  npm_mirror: "",
  linux_mirror: "",
});
const mirrorMeta = ref<MirrorsResponse>({
  pip_mirror: "",
  npm_mirror: "",
  linux_mirror: "",
  linux_package_manager: "",
  linux_distribution: "",
  linux_mirror_supported: false,
  linux_mirror_label: "Linux",
  linux_mirror_message: "",
});
let mounted = false;

const searchKeyword = ref("");
const statusFilter = ref("");

/**
 * 只套了搜索词、还没套状态筛选的列表。
 *
 * 状态标签页上的角标要数的就是它：点某个状态标签 = 在这份列表上再加一层状态过滤，
 * 所以角标数字和点进去看到的条数天然一致。
 * 如果角标改成直接数 depsList（无视搜索词），搜索状态下就会出现
 * 「失败 3」点进去却是空列表的自相矛盾。
 */
const searchScopedDeps = computed(() => {
  if (!searchKeyword.value) return depsList.value;
  const kw = searchKeyword.value.toLowerCase();
  return depsList.value.filter((dep) => dep.name?.toLowerCase().includes(kw));
});

const allCount = computed(() => searchScopedDeps.value.length);
const failedCount = computed(
  () => searchScopedDeps.value.filter((dep) => dep.status === "failed").length,
);
const installedCount = computed(
  () =>
    searchScopedDeps.value.filter((dep) => dep.status === "installed").length,
);

const filteredDepsList = computed(() => {
  const list = searchScopedDeps.value;
  if (!statusFilter.value) return list;
  return list.filter((dep) => dep.status === statusFilter.value);
});

const paginatedDepsList = computed(() => {
  const start = (depsPage.value - 1) * depsPageSize.value;
  return filteredDepsList.value.slice(start, start + depsPageSize.value);
});

const depsTotal = computed(() => filteredDepsList.value.length);
const depsPage = ref(1);
const depsPageSize = ref(20);

// 移动端不渲染任何状态筛选控件（桌面的 el-select 与「全部 / 已安装 / 失败」分段都在 v-else 分支里）。
// 从桌面缩窗进来时若还挂着筛选，就成了一个看不见也关不掉的过滤条件，所以进入移动端时清空，默认显示全部。
// 勾选两个方向都清空：桌面的勾选在 el-table 里、移动端在卡片复选框上，换一套 UI 后对不上 ——
// 例如切回桌面时新挂上的 el-table 一行都没打勾，selectedIds 却还在，「批量卸载」凭空亮着。
watch(isMobile, (mobile) => {
  selectedIds.value = [];
  if (mobile && statusFilter.value) {
    statusFilter.value = "";
    depsPage.value = 1;
  }
});

// 移动端的勾选不经过 el-table。桌面上换页、搜索、轮询刷新时，el-table 会把不在当前数据里的行
// 自动剔出选择（经 selection-change 回到 handleSelectionChange）；移动卡片没有这一层，
// 不补的话翻页后批量栏还挂着上一页的勾选，批量卸载会作用到看不见的依赖。所以照着裁剪到当前页可见的行。
watch(paginatedDepsList, (rows) => {
  if (!isMobile.value || selectedIds.value.length === 0) return;
  const visibleIds = new Set(rows.map((row) => row.id));
  const next = selectedIds.value.filter((id) => visibleIds.has(id));
  if (next.length !== selectedIds.value.length) selectedIds.value = next;
});

// 翻页后回到顶部（O3，桌面与移动端都做）。本页是前端分页，数据已经在手上，等 DOM 换完再滚。
// 🔴 只挂在分页器的 current-change / size-change 上，不能挂进 loadData 或 watch(depsPage)：
// 3 秒轮询也会调 loadData，用户正往下看着会被一把拽回顶部。
const pageRootRef = ref<HTMLElement>();

async function handlePageChange() {
  await nextTick();
  scrollListToTop(pageRootRef.value);
}

async function handlePageSizeChange() {
  depsPage.value = 1;
  await nextTick();
  scrollListToTop(pageRootRef.value);
}

function resolveDisplayPythonVersion(
  runtimes: PythonRuntimeInfo[],
  defaultVersion: string,
) {
  if (runtimes.length === 0) {
    return defaultVersion || "3.12";
  }

  const defaultRuntime = runtimes.find((item) => item.version === defaultVersion);
  if (defaultRuntime?.available) {
    return defaultRuntime.version;
  }

  const firstAvailableRuntime = runtimes.find((item) => item.available);
  if (firstAvailableRuntime) {
    return firstAvailableRuntime.version;
  }

  const firstRuntime = runtimes[0];
  return defaultRuntime?.version || firstRuntime?.version || defaultVersion || "3.12";
}

function statusType(status: string) {
  switch (status) {
    case "queued":
      return "warning";
    case "installed":
      return "success";
    case "installing":
      return "warning";
    case "removing":
      return "warning";
    case "cancelled":
      return "info";
    case "failed":
      return "danger";
    default:
      return "info";
  }
}

function statusLabel(status: string) {
  switch (status) {
    case "queued":
      return "排队中";
    case "installed":
      return "已安装";
    case "installing":
      return "安装中";
    case "removing":
      return "卸载中";
    case "cancelled":
      return "已取消";
    case "failed":
      return "失败";
    default:
      return status;
  }
}

function isProcessing(status: string) {
  return (
    status === "queued" || status === "installing" || status === "removing"
  );
}

const hasPendingDeps = computed(() =>
  depsList.value.some((dep) => isProcessing(dep.status)),
);

watch([hasPendingDeps, isPageActive], () => {
  syncPendingRefresh();
});

const linuxMirrorLabel = computed(
  () => mirrorMeta.value.linux_mirror_label || "Linux",
);
const linuxMirrorSupported = computed(
  () => mirrorMeta.value.linux_mirror_supported,
);
const linuxMirrorMessage = computed(
  () => mirrorMeta.value.linux_mirror_message || "",
);
const linuxMirrorManagerText = computed(
  () => mirrorMeta.value.linux_package_manager || "未识别",
);
const linuxMirrorDistributionText = computed(
  () => mirrorMeta.value.linux_distribution || "",
);
// 这份 Linux 清单连同模板里 pip / npm 两个「快捷选择」下拉，在 APP 的
// lib/features/deps/views/dep_list_page.dart 里有一份手写副本。增删候选源或挪「(默认)」时两边一起改：
// APP 那份从首版起半年没同步过，直到 issue #150 才补齐。
const linuxMirrorOptions = computed(() => {
  const manager = mirrorMeta.value.linux_package_manager;
  const distro = mirrorMeta.value.linux_distribution;

  if (manager === "apk") {
    return [
      // issue #146 / v3.3.2：默认源从阿里云换成腾讯云（阿里云限速）。
      // 「(默认)」必须跟着后端的 defaultLinuxMirror 一起挪，否则 UI 会出现
      // 「阿里云 (默认)」但实际默认是别家的自相矛盾。阿里云保留为普通候选。
      { label: "阿里云", value: "https://mirrors.aliyun.com/alpine" },
      {
        label: "清华大学",
        value: "https://mirrors.tuna.tsinghua.edu.cn/alpine",
      },
      {
        label: "腾讯云 (默认)",
        value: "https://mirrors.cloud.tencent.com/alpine",
      },
      { label: "华为云", value: "https://repo.huaweicloud.com/alpine" },
      { label: "中科大", value: "https://mirrors.ustc.edu.cn/alpine" },
    ];
  }

  if (manager === "apt") {
    if (distro === "debian") {
      return [
        // issue #146 / v3.3.2：默认源换腾讯云，阿里云降为普通候选；
        // 另按 issue 建议补上华为云与天翼云。
        // v3.3.3 / issue #150：天翼云去掉 v3.3.2 加的未验证标注。当初标它，是因为 Debian 的 security 段
        // 由后端 resolveAPTMirrorURI 自动拼成 <源>-security，而 mirrors.ctyun.cn/debian-security
        // 在开发机上一直测不通（后来查明是本机 HTTP 代理断连，不是源的问题）。现在的依据：
        // 用户在 bookworm 容器里用天翼云 apt-get update 与装包全程成功，日志里有
        // debian-security 的 bookworm-security InRelease；本机绕过代理后也验过 debian、
        // debian-security、ubuntu 的 InRelease 是真实签名正文，pip 下拉里的天翼云 pypi 能 pip download。
        // 天翼云没有 Alpine 与 npm 镜像（alpine 路径回的是镜像站首页），apk 档和 npm 下拉都不要加。
        {
          label: "阿里云 Debian",
          value: "https://mirrors.aliyun.com/debian",
        },
        {
          label: "清华大学 Debian",
          value: "https://mirrors.tuna.tsinghua.edu.cn/debian",
        },
        {
          label: "腾讯云 Debian (默认)",
          value: "https://mirrors.cloud.tencent.com/debian",
        },
        {
          label: "华为云 Debian",
          value: "https://repo.huaweicloud.com/debian",
        },
        {
          label: "天翼云 Debian",
          value: "https://mirrors.ctyun.cn/debian",
        },
      ];
    }
    return [
      {
        label: "阿里云 Ubuntu",
        value: "https://mirrors.aliyun.com/ubuntu",
      },
      {
        label: "清华大学 Ubuntu",
        value: "https://mirrors.tuna.tsinghua.edu.cn/ubuntu",
      },
      {
        label: "腾讯云 Ubuntu (默认)",
        value: "https://mirrors.cloud.tencent.com/ubuntu",
      },
      { label: "华为云 Ubuntu", value: "https://repo.huaweicloud.com/ubuntu" },
      // 天翼云 Ubuntu 与 Debian 档对称，#150 起同样去掉未验证标注：本机实测 noble、noble-security、
      // noble-updates、jammy 的 InRelease 均为真实签名正文（只验到这一步，没在 Ubuntu 容器里实装过包）。
      {
        label: "天翼云 Ubuntu",
        value: "https://mirrors.ctyun.cn/ubuntu",
      },
    ];
  }

  return [];
});

/**
 * Linux 页签「安装编译工具链」要预填的包名。
 *
 * 名字按包管理器分：Alpine 的 build-base 在 Debian 系叫 build-essential，
 * RPM 系两边都不是。探测不到包管理器时返回空数组 —— 此时给任何包名都是错的，
 * 按钮会被禁用（见 linuxToolchainTip 说明原因）。
 * 走的是现成的 POST /deps，不新增接口，装出来的记录和手动新增的完全一样。
 */
const linuxToolchainPackages = computed<string[]>(() => {
  switch (mirrorMeta.value.linux_package_manager) {
    case "apk":
      return ["build-base", "linux-headers", "cmake"];
    case "apt":
      return ["build-essential", "cmake"];
    case "dnf":
    case "yum":
    case "microdnf":
    case "zypper":
      return ["gcc", "gcc-c++", "make", "cmake"];
    default:
      return [];
  }
});

const linuxToolchainTip = computed(() => {
  if (linuxToolchainPackages.value.length === 0) {
    return "未识别到系统包管理器，无法确定对应的工具链包名";
  }
  return `将预填：${linuxToolchainPackages.value.join(" ")}`;
});

/** 新建弹窗里 Linux 分支的前置提示：权限 + 当前包管理器 */
const linuxCreateHint = computed(() => {
  const manager = mirrorMeta.value.linux_package_manager;
  const managerText = manager
    ? `当前检测到的包管理器：${manager}`
    : "当前未识别到系统包管理器，提交后大概率直接失败";
  return `安装的是系统软件包，需要面板进程有 root 权限；非 root 部署会安装失败。${managerText}。`;
});

/** 名称输入框的占位文案。Linux 下给真实包名示例，通用文案对系统包没有任何提示作用 */
const createNamePlaceholder = computed(() => {
  if (createType.value !== "linux") {
    return "每行一个依赖名称，支持换行/空格/逗号分隔";
  }
  const manager = mirrorMeta.value.linux_package_manager;
  if (manager === "apk") {
    return "每行一个系统包名，例如：build-base linux-headers cmake";
  }
  if (manager === "apt") {
    return "每行一个系统包名，例如：build-essential cmake";
  }
  return "每行一个系统包名，例如：gcc make cmake";
});

function openLinuxToolchainInstall() {
  if (linuxToolchainPackages.value.length === 0) return;
  createType.value = "linux";
  createNames.value = linuxToolchainPackages.value.join("\n");
  // 预填的是多行包名，自动拆分必须开着，否则会被当成一个超长的包名提交
  autoSplit.value = true;
  showCreateDialog.value = true;
}

// ---------- Playwright 一键安装（v3.3.1，issue #142）----------
// 入口两处：Linux 页签说明块里的按钮、工具栏菜单里的同名项。能不能装、装到哪一步全听
// GET /deps/playwright（支持与否由后端按发行版 / 架构 / 是否 root 判定，前端不再自己猜）。
// 接口拿不到（老服务端没有、网络失败）时安静降级：按钮置灰、旁边写明原因，不弹错。
const playwrightStatus = ref<PlaywrightStatus | null>(null);
const playwrightStatusFailed = ref(false);
const playwrightInstalling = ref(false);
// 状态请求的序号：挂载与切页签可能连发两次，只认最后一次的结果，免得旧响应晚到把新状态盖回去
let playwrightStatusSeq = 0;

const playwrightSupported = computed(
  () => playwrightStatus.value?.supported === true,
);

/** 三项都齐了才算就绪：系统库全部装上、默认 Python 里有 playwright 包、数据卷里有 Chromium */
const playwrightReady = computed(() => {
  const status = playwrightStatus.value;
  return (
    !!status &&
    status.supported &&
    status.python_installed &&
    status.browsers_installed &&
    status.linux_total > 0 &&
    status.linux_installed >= status.linux_total
  );
});

/** 按钮旁的说明：不可用时写原因，可用时写三项进度（就绪时模板里另加「已就绪」前缀） */
const playwrightTip = computed(() => {
  if (playwrightStatusFailed.value) return "无法获取 Playwright 环境状态";
  const status = playwrightStatus.value;
  if (!status) return "正在检测 Playwright 运行环境…";
  if (!status.supported) {
    return status.reason || "当前环境不支持一键安装 Playwright";
  }
  return [
    `系统库 ${status.linux_installed}/${status.linux_total} 已安装`,
    `playwright ${status.python_installed ? "已安装" : "未安装"}`,
    `浏览器${status.browsers_installed ? "已下载" : "未下载"}`,
  ].join(" · ");
});

async function loadPlaywrightStatus() {
  const seq = ++playwrightStatusSeq;
  try {
    const res = await depsApi.playwrightStatus();
    if (seq !== playwrightStatusSeq) return;
    // 按形状校验一次：拿到的不是这份结构（如被反代换成了别的响应）就按「拿不到」处理，别渲染出 undefined
    if (!res || typeof res.supported !== "boolean") {
      throw new Error("unexpected playwright status payload");
    }
    playwrightStatus.value = res;
    playwrightStatusFailed.value = false;
  } catch {
    if (seq !== playwrightStatusSeq) return;
    playwrightStatus.value = null;
    playwrightStatusFailed.value = true;
  }
}

async function handlePlaywrightInstall() {
  const status = playwrightStatus.value;
  if (!status?.supported || playwrightInstalling.value) return;
  const packageText =
    status.linux_total > 0 ? `${status.linux_total} 个系统包` : "一批系统包";
  const browserText = status.browsers_path
    ? `把 Chromium 下载到数据卷（${status.browsers_path}）`
    : "下载 Chromium 浏览器";
  try {
    await ElMessageBox.confirm(
      h("div", [
        h("p", "需要面板以 root 身份运行。"),
        h("p", `将登记 ${packageText}，并${browserText}。`),
        h("p", "容器部署下，重建容器后系统包会在后台自动重装。"),
      ]),
      "安装 Playwright 运行环境",
      { confirmButtonText: "开始安装", cancelButtonText: "取消" },
    );
  } catch {
    return;
  }
  playwrightInstalling.value = true;
  try {
    const res = await depsApi.installPlaywright();
    const queued = Array.isArray(res.data) ? res.data : [];
    if (queued.length > 0) {
      ElMessage.success(`已加入安装队列，共 ${queued.length} 项`);
    } else {
      // 全部跳过（已就绪或正在处理中）时后端也回 201、data 为空，message 里写明了跳过几项；
      // 这时再说「已加入安装队列，共 0 项」像是什么都没发生，直接用后端的原话
      ElMessage.info(res.message || "没有需要加入队列的项：都已就绪或正在处理中");
    }
    // 系统库先装、playwright 包后装，都是按顺序排队的依赖记录：切到 Linux 页签就能看到进度与各自的日志
    switchDepType("linux");
    // switchDepType 只拉到刚入队时的状态快照，之后由低频跟踪把按钮旁的三项进度推到最终结果
    startPlaywrightWatch(queued);
  } catch (err: any) {
    // 不支持（非 root、非 Debian 12 等）时后端回 400 {error}，原样展示给用户
    ElMessage.error(err?.response?.data?.error || "安装 Playwright 运行环境失败");
  } finally {
    playwrightInstalling.value = false;
  }
}

// 按钮旁的状态只在挂载、切到 Linux、手动刷新时拉，装的过程中会过期。补三处自动刷新，但都不挂到
// 3 秒一次的 loadData 轮询上：GET /deps/playwright 每次都要对清单里已登记的包逐个跑 dpkg-query
// （最多 26 个子进程）外加一次 pip show，跟着 3 秒轮询跑太重。

// ① Linux 页签上的排队 / 安装全部跑完的那一刻补拉一次：「系统库 x/y 已安装」这时才会变。
// 只认「有 → 没有」这一次迁移，停在别的页签、页面失活时不拉（回来时 switchDepType / 下面 ③ 会拉）。
watch(hasPendingDeps, (pending, wasPending) => {
  if (
    wasPending &&
    !pending &&
    activeTab.value === "linux" &&
    isPageActive.value
  ) {
    void loadPlaywrightStatus();
  }
});

// ② 一键安装之后的低频跟踪。hasPendingDeps 只看当前页签的列表：Linux 包装完它就停了，可后台协程还在接着装
// Python 的 playwright、下载 Chromium（最慢、最容易失败的一步，记录在 Python 页签），光靠 ① 看不到这一段。
// 所以点完一键安装后，停在 Linux 页签且页面可见时每 8 秒拉一次状态，顺带看 Python 那条记录的结局：
//   - 装完（playwright 包与浏览器都就位，或那条记录已是 installed）、失败、取消 → 收手；失败时提示去 Python 页签看日志；
//   - 离开 Linux 页签 / 页面失活只是暂停；回来时立即补跑一轮（查记录、拉状态、该收手就收手），不干等 8 秒；
//   - 手里有 Python 记录的 id、它还在排队 / 安装时一直跟到它出结局，不设时限：后端保证它会走到终态
//     （记录自带操作超时，默认 20 分钟、从它开跑才起算，慢网下载 Chromium 时结局完全可能出在点击 15 分钟之后；
//     面板重启后启动校验也会把遗留的排队 / 安装中记录收口），前端到点就停反而看不到这一步的成败；
//   - 拿不到这条记录的 id（入队时被跳过、按版本查不到）时才用 15 分钟兜底，免得某个系统库失败导致
//     永远凑不齐「已就绪」、一直轮询下去。兜底收手的那一轮照样先拉过状态，按钮旁停在最后一次的结果上。
const PLAYWRIGHT_WATCH_INTERVAL_MS = 8000;
const PLAYWRIGHT_WATCH_MAX_MS = 15 * 60 * 1000;
const playwrightWatching = ref(false);
// 从点击起算的 15 分钟兜底，只在 playwrightWatchPythonDep 为 null 时生效
let playwrightWatchDeadline = 0;
let playwrightWatchTimer: ReturnType<typeof setInterval> | null = null;
// 本次入队的那条 Python playwright 记录。它被当作「正在处理中」跳过（data 里没有）时为 null，只跟状态、不查结局，
// 这时才受 15 分钟兜底约束
let playwrightWatchPythonDep: { id: number; version: string } | null = null;
// 上一轮还没回来就不叠请求：dpkg-query 慢的机器上一轮可能超过 8 秒
let playwrightWatchTicking = false;

function startPlaywrightWatch(queued: any[]) {
  const pythonDep = queued.find((dep) => dep?.type === "python");
  playwrightWatchPythonDep = pythonDep
    ? {
        id: pythonDep.id,
        version: pythonDep.python_version || pythonDefaultVersion.value,
      }
    : null;
  playwrightWatchDeadline = Date.now() + PLAYWRIGHT_WATCH_MAX_MS;
  playwrightWatching.value = true;
  // 不立即跑一轮：switchDepType 刚拉过状态快照，Python 记录也才入队，立即再查只是把那次重型请求多发一遍
  syncPlaywrightWatch(false);
}

function clearPlaywrightWatchTimer() {
  if (playwrightWatchTimer) {
    clearInterval(playwrightWatchTimer);
    playwrightWatchTimer = null;
  }
}

function stopPlaywrightWatch() {
  playwrightWatching.value = false;
  playwrightWatchPythonDep = null;
  clearPlaywrightWatchTimer();
}

// 这里不判断时限：暂停期间过了 15 分钟也只是保持暂停（没有定时器，不花任何请求）。
// 是否收手统一交给 tick——它先查记录、拉状态再决定，收手前按钮旁一定是最新结果。
function syncPlaywrightWatch(tickNow = true) {
  if (
    playwrightWatching.value &&
    isPageActive.value &&
    activeTab.value === "linux"
  ) {
    if (!playwrightWatchTimer) {
      playwrightWatchTimer = setInterval(() => {
        void tickPlaywrightWatch();
      }, PLAYWRIGHT_WATCH_INTERVAL_MS);
      // 从暂停恢复（切回 Linux 页签、页面重新可见）时立即跑一轮：离开期间 Python 那一步可能已有结局，
      // 让用户马上看到，不用再等 8 秒
      if (tickNow) void tickPlaywrightWatch();
    }
    return;
  }
  clearPlaywrightWatchTimer();
}

async function tickPlaywrightWatch() {
  if (playwrightWatchTicking) return;
  playwrightWatchTicking = true;
  try {
    // 先看 Python 那条记录（只查库，便宜），再拉状态：记录一到终态就收手，收手前拉的这次状态正好是最终结果
    const pythonDep = playwrightWatchPythonDep;
    let pythonStatus = "";
    if (pythonDep) {
      const res = await depsApi.list("python", pythonDep.version);
      if (!playwrightWatching.value || playwrightWatchPythonDep !== pythonDep) {
        return;
      }
      const record = (res.data || []).find((dep) => dep.id === pythonDep.id);
      if (record) {
        pythonStatus = record.status;
      } else {
        // 按版本查不到（比如老记录的 python_version 为空、归到了别的版本下）：不再查它，只跟状态，15 分钟兜底
        playwrightWatchPythonDep = null;
      }
    }
    await loadPlaywrightStatus();
    // 等待期间可能已收手（状态已就位、页面卸载），也可能又点了一次一键安装开了新一轮：
    // 旧一轮读到的记录状态不能拿去结束新一轮
    if (!playwrightWatching.value) return;
    if (pythonDep && playwrightWatchPythonDep !== pythonDep) return;
    // Python 排在队尾：它到了终态，前面的系统库也都跑过了
    if (pythonStatus && !isProcessing(pythonStatus)) {
      stopPlaywrightWatch();
      if (pythonStatus === "failed") {
        ElMessage.warning(
          "Playwright 安装失败：请到 Python3 页签查看 playwright 的安装日志",
        );
      }
      return;
    }
    // 15 分钟兜底放在拉完状态之后判断：收手前按钮旁已是这一刻的结果。手里有记录 id 时不走这里——
    // 它还在排队 / 安装，就等后端给出终态。时限读的是当前那一轮的，旧一轮不会按自己的时限误停新一轮
    if (!playwrightWatchPythonDep && Date.now() >= playwrightWatchDeadline) {
      stopPlaywrightWatch();
    }
  } catch {
    // 能抛到这里的只有查 Python 记录那一步（loadPlaywrightStatus 自己吞错）。网络抖一下不收手，下一轮再试；
    // 也不因此按时限收手：没查到记录状态，不能当作它已不在处理中
  } finally {
    playwrightWatchTicking = false;
  }
}

// 状态无论从哪条路径刷新（跟踪、切页签、手动刷新），playwright 包与浏览器都就位就说明队尾已经跑完，
// 不必再等下一轮。这一条也覆盖「Python 记录被跳过、手里没有它的 id」的情况。
// 刚点完一键安装时不会误判：那条记录已被置为排队（或本来就在处理中），而 python_installed 只认
// 登记为已安装的记录，这时是 false。
watch(playwrightStatus, (status) => {
  if (
    playwrightWatching.value &&
    status?.python_installed &&
    status.browsers_installed
  ) {
    stopPlaywrightWatch();
  }
});

watch([playwrightWatching, isPageActive, activeTab], () => {
  syncPlaywrightWatch();
});

// ③ 页面重新可见时补拉一次：切回这个浏览器标签页（visibilitychange 不会触发 onActivated），或从别的菜单页
// 切回来（keep-alive 二次进页只触发 activated），isPageActive 由 false 变 true 两种都覆盖。
// 不论跟踪还在不在都要有这一拉——跟踪已收手（或从没开过）时，离开期间装完 / 失败的结果只能靠它看到。
// 跟踪还在时由上面 syncPlaywrightWatch 恢复定时器那一下立即跑的 tick 来拉，这里不重复发这个重型请求。
watch(isPageActive, (active, wasActive) => {
  if (
    active &&
    !wasActive &&
    activeTab.value === "linux" &&
    !playwrightWatching.value
  ) {
    void loadPlaywrightStatus();
  }
});
// ---------- /Playwright ----------

async function loadData() {
  loading.value = true;
  try {
    const res = await depsApi.list(
      activeTab.value,
      activeTab.value === "python" ? pythonVersion.value : undefined,
    );
    depsList.value = res.data || [];
    // 三个类型页签上的失败数由服务端一并带回（跨类型全量，python 跨所有版本）。
    // 老版本服务端没有这个字段，读不到就退回全 0，别把 undefined 渲染出去。
    failedByType.value = res.failed_by_type || { nodejs: 0, python: 0, linux: 0 };
    selectedIds.value = selectedIds.value.filter((id) =>
      depsList.value.some((dep) => dep.id === id),
    );
    syncCurrentLogRow();
    syncPendingRefresh();
  } catch {
    if (!refreshTimer) {
      depsList.value = [];
    }
    // failedByType 刻意不清零：网络抖一下就把「有 3 个失败」的提示抹掉，
    // 比不显示更糟——失败数保留上一次的值，只在请求成功时整体替换。
    syncPendingRefresh();
  } finally {
    loading.value = false;
  }
}

function stopRefreshTimer() {
  if (refreshTimer) {
    clearInterval(refreshTimer);
    refreshTimer = null;
  }
}

function syncPendingRefresh() {
  if (hasPendingDeps.value && isPageActive.value) {
    if (!refreshTimer) {
      refreshTimer = setInterval(() => {
        void loadData();
      }, 3000);
    }
    return;
  }
  stopRefreshTimer();
}

async function loadPythonRuntimes() {
  try {
    const res = await depsApi.pythonRuntimes();
    pythonRuntimes.value = res.data || [];
    pythonDefaultVersion.value = res.default_version || "3.12";
    pythonVersion.value = resolveDisplayPythonVersion(
      pythonRuntimes.value,
      pythonDefaultVersion.value,
    );
    createPythonVersion.value = pythonVersion.value;
  } catch {
    pythonRuntimes.value = [
      {
        version: "3.10",
        label: "Python 3.10",
        default: false,
        venv_path: "",
        venv_healthy: false,
        python_path: "",
        pip_path: "",
        available: false,
        message: "",
      },
      {
        version: "3.11",
        label: "Python 3.11",
        default: false,
        venv_path: "",
        venv_healthy: false,
        python_path: "",
        pip_path: "",
        available: false,
        message: "",
      },
      {
        version: "3.12",
        label: "Python 3.12",
        default: true,
        venv_path: "",
        venv_healthy: false,
        python_path: "",
        pip_path: "",
        available: false,
        message: "",
      },
    ];
    pythonDefaultVersion.value = "3.12";
    pythonVersion.value = resolveDisplayPythonVersion(
      pythonRuntimes.value,
      pythonDefaultVersion.value,
    );
    createPythonVersion.value = pythonVersion.value;
  }
}

function handlePythonVersionChange() {
  depsPage.value = 1;
  createPythonVersion.value = pythonVersion.value;
  void loadData();
}

async function setCurrentPythonDefault() {
  try {
    const res = await depsApi.setDefaultPythonRuntime(pythonVersion.value);
    pythonDefaultVersion.value = res.default_version || pythonVersion.value;
    pythonRuntimes.value = pythonRuntimes.value.map((item) => ({
      ...item,
      default: item.version === pythonDefaultVersion.value,
    }));
    pythonVersion.value = resolveDisplayPythonVersion(
      pythonRuntimes.value,
      pythonDefaultVersion.value,
    );
    createPythonVersion.value = pythonVersion.value;
    ElMessage.success("默认 Python 版本已更新");
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.error || "设置默认 Python 版本失败");
  }
}

function parseNames(text: string): string[] {
  if (!autoSplit.value) return [text.trim()].filter(Boolean);
  // 方括号里的逗号 / 空白不切（issue #166）：httpx[http2,socks] 是一个包带两个 extras，
  // 以前会被切成 httpx[http2 和 socks] 两个坏名字。
  // 换行无论如何都先切开（与 APP 的 splitDependencyNames 同一口径）：用户漏写 ] 时，
  // 不能把后面几行吞进同一个名字里提交上去。
  // 行内的前瞻表示「往后先遇到 ] 而不是 [」，即当前分隔符落在一对方括号里面。
  return text
    .split(/\r?\n/)
    .flatMap((line) => line.split(/[,\s]+(?![^[]*\])/))
    .map((s) => s.trim())
    .filter(Boolean);
}

/** 「新增依赖」：桌面 Split Button 主体与移动端「+」共用，新建弹窗的类型默认取当前页签 */
function openCreateDialog() {
  createType.value = activeTab.value;
  showCreateDialog.value = true;
}

async function handleCreate() {
  const names = parseNames(createNames.value);
  if (names.length === 0) {
    ElMessage.warning("请输入依赖名称");
    return;
  }
  creating.value = true;
  try {
    await depsApi.create(
      createType.value,
      names,
      createType.value === "python" ? createPythonVersion.value : undefined,
    );
    ElMessage.success(
      createType.value === "python"
        ? `已提交 ${names.length} 个依赖到 ${pythonRuntimes.value.length || 1} 个 Python 版本安装`
        : `已提交 ${names.length} 个依赖安装`,
    );
    showCreateDialog.value = false;
    createNames.value = "";
    activeTab.value = createType.value;
    if (activeTab.value === "python")
      pythonVersion.value = createPythonVersion.value;
    loadData();
  } catch {
    ElMessage.error("提交安装失败");
  } finally {
    creating.value = false;
  }
}

function handleSelectionChange(rows: any[]) {
  selectedIds.value = rows.map((r) => r.id);
}

function isSelected(id: number) {
  return selectedIdSet.value.has(id);
}

function toggleSelected(id: number, checked: boolean | string | number) {
  const next = new Set(selectedIds.value);
  if (checked) {
    next.add(id);
  } else {
    next.delete(id);
  }
  selectedIds.value = [...next];
}

// 移动端批量栏的「全选 / 取消全选」：只作用于当前页的卡片，与桌面表头复选框的范围一致
// （勾选本来就会被裁剪到当前页，见 watch(paginatedDepsList)，所以不存在跨页的全选）。
const allSelectedOnPage = computed(
  () =>
    paginatedDepsList.value.length > 0 &&
    paginatedDepsList.value.every((dep) => selectedIdSet.value.has(dep.id)),
);

function toggleSelectAllOnPage() {
  selectedIds.value = allSelectedOnPage.value
    ? []
    : paginatedDepsList.value.map((dep) => dep.id);
}

// 桌面表格实例：勾选态保存在 el-table 内部，「取消选择」与批量操作提交后需同步清空表格勾选
const depsTableRef = ref<any>(null);

function clearSelection() {
  depsTableRef.value?.clearSelection?.();
  selectedIds.value = [];
}

async function handleBatchDelete() {
  if (selectedIds.value.length === 0) return;
  try {
    await ElMessageBox.confirm(
      `确定批量卸载选中的 ${selectedIds.value.length} 个依赖？`,
      "批量卸载",
      { type: "warning" },
    );
    await depsApi.batchDelete(selectedIds.value);
    ElMessage.success("批量卸载已提交");
    clearSelection();
    loadData();
  } catch (err: any) {
    if (err !== "cancel" && err?.toString() !== "cancel") {
      ElMessage.error(err?.response?.data?.error || "批量卸载失败");
    }
  }
}

async function handleBatchReinstall() {
  if (selectedIds.value.length === 0) return;
  if (batchReinstallIds.value.length === 0) {
    ElMessage.warning("选中的依赖当前都在处理中，暂时无法重装");
    return;
  }

  const skippedCount =
    selectedIds.value.length - batchReinstallIds.value.length;
  const skipHint =
    skippedCount > 0
      ? `\n其中 ${skippedCount} 个依赖正在处理中，已自动跳过。`
      : "";

  try {
    await ElMessageBox.confirm(
      `确定顺序重装选中的 ${batchReinstallIds.value.length} 个依赖吗？${skipHint}`,
      "批量重装",
      { type: "warning" },
    );
    await depsApi.batchReinstall(batchReinstallIds.value);
    ElMessage.success(
      `已提交 ${batchReinstallIds.value.length} 个依赖顺序重装`,
    );
    // 批量重装提交后同步清空桌面表格与移动端的勾选态，回到常规工具栏
    clearSelection();
    loadData();
  } catch (err: any) {
    if (err !== "cancel" && err?.toString() !== "cancel") {
      ElMessage.error(err?.response?.data?.error || "批量重装失败");
    }
  }
}

async function handleDelete(row: any) {
  try {
    await ElMessageBox.confirm(`确认卸载 ${row.name}？`, "提示", {
      type: "warning",
    });
  } catch {
    return;
  }
  try {
    await depsApi.delete(row.id);
    ElMessage.success("卸载中");
    loadData();
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.error || "卸载失败");
  }
}

async function handleForceDelete(row: any) {
  try {
    await ElMessageBox.confirm(
      `确认强制卸载 ${row.name}？\n强制卸载会跳过依赖检查直接删除`,
      "强制卸载",
      { type: "warning" },
    );
  } catch {
    return;
  }
  try {
    await depsApi.delete(row.id, true);
    ElMessage.success("强制卸载中");
    loadData();
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.error || "强制卸载失败");
  }
}

async function handleReinstall(row: any) {
  try {
    await depsApi.reinstall(row.id);
    ElMessage.success("重新安装中");
    loadData();
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.error || "操作失败");
  }
}

async function handleExport() {
  exporting.value = true;
  try {
    const blob = await depsApi.exportList(
      activeTab.value,
      activeTab.value === "python" ? pythonVersion.value : undefined,
    );
    const url = window.URL.createObjectURL(blob);
    const anchor = document.createElement("a");
    const timestamp = new Date()
      .toISOString()
      .slice(0, 19)
      .replace(/[-:T]/g, "");
    anchor.href = url;
    const typeName =
      activeTab.value === "python"
        ? `${activeTab.value}-${pythonVersion.value.replace(".", "")}`
        : activeTab.value;
    anchor.download = `dependencies-${typeName}-${timestamp}.txt`;
    document.body.appendChild(anchor);
    anchor.click();
    document.body.removeChild(anchor);
    window.URL.revokeObjectURL(url);
    // v3.3.3 / issue #150：后端改为按安装时填写的原样导出（不再附带「==>已装版本」），
    // 提示里点明能直接粘回「新建依赖」，免得用户再手动把版本号补上、把原本没钉版本的依赖钉死。
    ElMessage.success(
      "依赖清单已导出（与安装时填写的一致，可直接粘贴回「新建依赖」）",
    );
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.error || "导出失败");
  } finally {
    exporting.value = false;
  }
}

async function handleCancel(row: any) {
  try {
    await depsApi.cancel(row.id);
    ElMessage.success("取消请求已提交");
    loadData();
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.error || "取消失败");
  }
}

// currentLogRow 原本只是打开弹窗那一刻的快照，列表刷新后不会跟着变，
// 于是后端把状态改成 failed 时弹窗里还停在旧状态。这里在每次拉列表后回填。
function syncCurrentLogRow() {
  const current = currentLogRow.value;
  if (!current) {
    return;
  }
  const fresh = depsList.value.find((dep) => dep.id === current.id);
  if (fresh) {
    currentLogRow.value = fresh;
    return;
  }
  // 卸载成功后后端会直接删掉这一行（deleteOnSuccess），列表里再也找不到它。
  // 不识别这种情况的话，弹窗会一直停在快照里的「卸载中」，
  // 「取消当前任务」按钮也会常亮，点下去必然 404。
  // 只在日志流已经结束后才认定为已删除，避免切换 tab 造成的误判。
  if (logDone.value) {
    logRowRemoved.value = true;
  }
}

function viewLog(row: any) {
  currentLogRow.value = row;
  logContent.value = "";
  logStreamNotice.value = "";
  logRowRemoved.value = false;
  logDone.value = !(row.status === "installing" || row.status === "removing");
  showLogDialog.value = true;

  closeSSE();

  if (logDone.value) {
    // 已结束记录：一次性加载、停在顶部，不跟随
    installFollow.end();
    depsApi
      .getStatus(row.id)
      .then((res) => {
        logContent.value = res.data?.log || "暂无日志";
      })
      .catch(() => {
        logContent.value = "获取日志失败";
      });
    return;
  }

  // 安装/卸载进行中：开启自动跟随
  installFollow.begin(true);
  const url = `/api/v1/deps/${row.id}/log-stream`;
  eventSource = openAuthorizedEventStream(url, {
    onMessage(data) {
      depsLogBuffer.push(data);
      if (!depsLogFlushRaf) {
        depsLogFlushRaf = requestAnimationFrame(() => {
          depsLogFlushRaf = 0;
          flushDepsLogBuffer();
        });
      }
    },
    onEvent(event) {
      if (event.event !== "done") {
        return;
      }
      logDone.value = true;
      // 先把还挂在 rAF 里的最后一批冲进去（按跟随态贴底），再冻结跟随态
      flushDepsLogBuffer();
      installFollow.end();
      closeSSE();
      // data 携带的是结束原因：真实终态（installed/failed/...）表示任务确实结束了；
      // timeout（服务端兜底的硬上限到点）与 reconnect（面板关停时服务端收流）只代表这条日志流断了，
      // 任务本身可能还在跑，不能当成结束。
      const reason = (event.data || "").trim();
      if (reason === "timeout" || reason === "reconnect") {
        logStreamNotice.value = "日志流已断开，任务可能仍在进行";
      }
      loadData();
    },
    onError() {
      logDone.value = true;
      flushDepsLogBuffer();
      installFollow.end();
      closeSSE();
      logStreamNotice.value = "日志流已断开，任务可能仍在进行";
      loadData();
    },
  });
}

// 把还挂在 rAF 里、没来得及 flush 的安装日志立刻冲进去。结束（done / onError）时必须先调它再 end()：
// 最后几行常与 done 同一帧到达，留给 rAF 的话那次 onContentChange 会落在 end() 之后被忽略，
// 跟随中的用户就看不到结尾那几行（旧实现在 rAF 里无条件贴底，没有这个问题）。
function flushDepsLogBuffer() {
  if (depsLogFlushRaf) {
    cancelAnimationFrame(depsLogFlushRaf);
    depsLogFlushRaf = 0;
  }
  if (depsLogBuffer.length === 0) return;
  logContent.value += depsLogBuffer.join("\n") + "\n";
  depsLogBuffer = [];
  // 跟随中贴到最新，暂停时什么都不做（由 useLogAutoFollow 判定）
  installFollow.onContentChange();
}

function closeSSE() {
  if (eventSource) {
    eventSource.close();
    eventSource = null;
  }
}

watch(showLogDialog, (val) => {
  if (!val) {
    closeSSE();
    currentLogRow.value = null;
    logStreamNotice.value = "";
    logRowRemoved.value = false;
  }
});

// Android 运行时安装日志：安装开始（名字非空）时开启跟随，结束时冻结
watch(androidInstallingName, (name) => {
  if (name) {
    androidFollow.begin(true);
  } else {
    androidFollow.end();
  }
});

// 安装日志是原地 push 的数组，靠长度变化触发跟随贴底
watch(
  () => androidInstallLog.value.length,
  () => {
    androidFollow.onContentChange();
  },
);

/**
 * 拉一次 GET /deps/mirrors 并写进 mirrorMeta，返回是否成功。
 *
 * 这份响应里除了三个镜像地址，还带着 linux_package_manager / linux_distribution /
 * linux_mirror_supported / linux_mirror_message —— 也就是「面板到底认出了 apk 还是 apt」。
 * 以前它只在打开镜像源弹窗时才拉，于是 Linux 页签和新建弹窗一个字都显示不出来。
 * 抽成函数后挂载时也调一次；镜像源弹窗仍然自己再拉一次拿最新值并回填表单，行为不变。
 */
async function loadMirrorMeta(): Promise<boolean> {
  try {
    mirrorMeta.value = await depsApi.getMirrors();
    return true;
  } catch {
    // 失败时保留上一次的值（初始值是全空 + 未识别），页面说明会退化成「未识别」，不弹错
    return false;
  }
}

async function openMirrorDialog() {
  showMirrorDialog.value = true;
  mirrorLoading.value = true;
  if (await loadMirrorMeta()) {
    // 这里回填的是后端返回的「生效值」：GET /deps/mirrors 目前只给 effective，
    // 区分不了「用户真存过这个值」还是「只是内置默认」。所以保存时不能拿回填值当用户意图，
    // 一律以下面这份快照做逐字段 diff（issue #146）。
    // 真要区分 configured / effective 得改后端 GET 的返回结构，本轮不动后端。
    mirrorForm.value.pip_mirror = mirrorMeta.value.pip_mirror || "";
    mirrorForm.value.npm_mirror = mirrorMeta.value.npm_mirror || "";
    mirrorForm.value.linux_mirror = mirrorMeta.value.linux_mirror || "";
  } else {
    ElMessage.error("获取镜像源配置失败");
  }
  // 快照必须在回填之后取，失败分支也要取：拉取失败时表单沿用上一次的值，
  // 这时同样只该把用户这次动过的字段提交上去。
  mirrorFormSnapshot.value = { ...mirrorForm.value };
  mirrorLoading.value = false;
}

async function handleSaveMirrors() {
  // 只提交「这次真改过」的字段。后端 SetMirrors 用 *string 区分「没传」和「传空串」，
  // 键不在 payload 里指针就是 nil，对应那条写盘分支整个不会执行。
  // 这样一次修掉两个用户必踩的 400（issue #146）：
  // 1. 只改 pip、Linux 那栏原样不动 —— 旧代码恒提交三个字段，后端 writeAPTMirror
  //    发现一个条目都没变会直接报「未找到可更新的 apt 软件源条目」，
  //    而此时 pip/npm 其实已经先写进去了，用户看到报错却以为没保存上；
  // 2. dnf/zypper 这类不支持镜像设置的系统上，空串照样被 PUT，触发「暂不支持镜像设置」。
  // 先把当前值拷一份定死：下面有 await，期间用户还能继续改输入框，
  // 拿快照比对、提交、回写快照必须始终用同一组值，否则会漏提交那次改动。
  const form = { ...mirrorForm.value };
  const snapshot = mirrorFormSnapshot.value;
  const payload: {
    pip_mirror?: string;
    npm_mirror?: string;
    linux_mirror?: string;
  } = {};
  if (form.pip_mirror !== snapshot.pip_mirror) {
    payload.pip_mirror = form.pip_mirror;
  }
  if (form.npm_mirror !== snapshot.npm_mirror) {
    payload.npm_mirror = form.npm_mirror;
  }
  const linuxChanged = form.linux_mirror !== snapshot.linux_mirror;
  if (linuxChanged) {
    payload.linux_mirror = form.linux_mirror;
  }

  // 拦截条件从「值非空」改成「值被改过」：不支持镜像设置的系统上输入框本来就是禁用的，
  // 值不会变，也就不会再拿一个没人动过的空串去撞后端那条报错。
  if (linuxChanged && !linuxMirrorSupported.value) {
    ElMessage.warning(
      linuxMirrorMessage.value || "当前系统暂不支持 Linux 镜像设置",
    );
    return;
  }

  if (Object.keys(payload).length === 0) {
    // 一个字段都没改就别发请求了：空 payload 后端虽然也回成功，但纯属白跑一趟
    ElMessage.info("镜像源未变更");
    showMirrorDialog.value = false;
    return;
  }

  mirrorSaving.value = true;
  try {
    await depsApi.setMirrors(payload);
    // 保存成功后把快照推到新值，弹窗即使不关也能继续正确 diff
    mirrorFormSnapshot.value = { ...form };
    ElMessage.success("镜像源设置成功");
    showMirrorDialog.value = false;
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.error || "设置失败");
  } finally {
    mirrorSaving.value = false;
  }
}

// 首字母 → 主题色令牌（作为 --dd-avatar 内联给 .dep-name-avatar），跟随主题切换
const letterColors: Record<string, string> = {
  a: "var(--dd-info)",
  b: "var(--dd-ok)",
  c: "var(--dd-warn)",
  d: "var(--dd-ok)",
  e: "var(--dd-err)",
  f: "var(--dd-text-3)",
  g: "var(--dd-info)",
  h: "var(--dd-accent-2)",
  i: "var(--dd-info)",
  j: "var(--dd-info)",
  k: "var(--dd-warn)",
  l: "var(--dd-ok)",
  m: "var(--dd-warn)",
  n: "var(--dd-info)",
  o: "var(--dd-accent-2)",
  p: "var(--dd-ok)",
  q: "var(--dd-err)",
  r: "var(--dd-accent-2)",
  s: "var(--dd-warn)",
  t: "var(--dd-info)",
  u: "var(--dd-ok)",
  v: "var(--dd-accent-2)",
  w: "var(--dd-warn)",
  x: "var(--dd-text-3)",
  y: "var(--dd-ok)",
  z: "var(--dd-err)",
};
function getLetterColor(name: string): string {
  const ch = (name || "?").charAt(0).toLowerCase();
  return letterColors[ch] || "var(--dd-info)";
}

// ---------- 按 query 定位（「需要关注」里依赖安装失败的条目跳过来）----------
// 链接形如 /deps?dep_id=<id>&keyword=<名称>&type=python|nodejs|linux：
// type 切页签、keyword 填搜索框、dep_id 把那一行翻到所在页、滚进可视区并高亮约 2 秒。
// 处理完立刻 router.replace 清掉 query，刷新页面不会再触发一次。
const route = useRoute();
const router = useRouter();
const locateHighlightId = ref(0);
let locateHighlightTimer: ReturnType<typeof setTimeout> | null = null;
// router.replace 是异步的，清掉之前 onActivated 与 watch(route.query) 可能先后读到同一份 query，用它挡第二次
let locateQueryConsuming = false;

/**
 * 读 query、改页面状态（页签 / 搜索词），并发起清 query。不拉数据。
 * 返回要定位的依赖 id（没有 dep_id 时为 0）；query 里没有定位参数、或正在处理上一份时返回 null。
 */
function applyLocateQuery(): number | null {
  if (locateQueryConsuming) return null;
  const query = route.query;
  const type = typeof query.type === "string" ? query.type : "";
  const keyword = typeof query.keyword === "string" ? query.keyword.trim() : "";
  const depId = Number(query.dep_id) || 0;
  const tab = depTypeTabs.find((item) => item.key === type);
  if (!tab && !keyword && depId <= 0) return null;

  locateQueryConsuming = true;
  void router.replace({ path: route.path }).finally(() => {
    locateQueryConsuming = false;
  });
  if (tab) activeTab.value = tab.key;
  if (keyword) searchKeyword.value = keyword;
  // 状态筛选清掉：目标多半是「失败」，挂着「已安装」筛选会把它藏起来
  statusFilter.value = "";
  depsPage.value = 1;
  return depId;
}

/** 列表已拉好之后：翻到目标所在页、滚进可视区、高亮约 2 秒 */
async function focusDepRow(depId: number) {
  let index = filteredDepsList.value.findIndex((dep) => dep.id === depId);
  // Python 列表只含当前选中的版本；失败的依赖可能装在别的版本下，逐个版本找一遍
  if (index < 0 && activeTab.value === "python") {
    for (const runtime of pythonRuntimes.value) {
      if (runtime.version === pythonVersion.value) continue;
      try {
        const res = await depsApi.list("python", runtime.version);
        if (!(res.data || []).some((dep: any) => dep.id === depId)) continue;
        pythonVersion.value = runtime.version;
        createPythonVersion.value = runtime.version;
        await loadData();
        break;
      } catch {
        // 某个版本拉失败不影响找其它版本
      }
    }
    index = filteredDepsList.value.findIndex((dep) => dep.id === depId);
  }
  if (index < 0) {
    ElMessage.info("列表里没有找到这条依赖，可能已被卸载");
    return;
  }
  depsPage.value = Math.floor(index / depsPageSize.value) + 1;
  locateHighlightId.value = depId;
  await nextTick();
  const target = pageRootRef.value?.querySelector(".is-locate-highlight");
  target?.scrollIntoView({
    block: "center",
    behavior: motionLevel() === "off" ? "auto" : "smooth",
  });
  if (locateHighlightTimer) clearTimeout(locateHighlightTimer);
  locateHighlightTimer = setTimeout(() => {
    locateHighlightId.value = 0;
    locateHighlightTimer = null;
  }, 2000);
}

/** 已挂载过的页面（keep-alive 再次进入、或就停在本页时 query 变了）：应用 query → 重新拉列表 → 定位 */
async function handleLocateQuery(): Promise<boolean> {
  const depId = applyLocateQuery();
  if (depId === null) return false;
  if (activeTab.value === "linux") void loadPlaywrightStatus();
  await loadData();
  if (depId > 0) await focusDepRow(depId);
  return true;
}

// 用户已经停在依赖页时，从铃铛弹出层再点一条：路由组件不重建，只有 query 变。
// 只在本页激活、路径就是本页时处理；从别的页跳过来那次由 onActivated 处理（那时 isViewActive 还是 false）。
watch(
  () => route.query,
  () => {
    if (!isViewActive.value || route.path !== "/deps") return;
    void handleLocateQuery();
  },
);
// ---------- /按 query 定位 ----------

onMounted(async () => {
  mounted = true;
  // 首次进页：先把 query 里的页签 / 搜索词应用上，下面的首拉直接拉对的页签，不会先拉一遍默认页签
  const locateDepId = applyLocateQuery();
  createType.value = activeTab.value;
  // 进页即把侧栏的「依赖管理」失败角标标记为已读——用户已经站在这一页上了，再红着没有意义
  badgesStore.ackDepsFailed();
  await loadPythonRuntimes();
  createPythonVersion.value = pythonVersion.value || pythonDefaultVersion.value;
  const firstLoad = loadData();
  if (locateDepId) void firstLoad.then(() => focusDepRow(locateDepId));
  loadAndroidStatus();
  // Linux 页签的说明、新建弹窗的提示与 placeholder 都要靠这份元数据，
  // 不能再等到用户打开镜像源弹窗才拉。失败也不弹错，页面自己退化成「未识别」。
  void loadMirrorMeta();
  // 工具栏菜单里的 Playwright 入口在任何页签都点得到，挂载时就要知道它能不能用
  void loadPlaywrightStatus();
});

onActivated(() => {
  // 角标清零刻意放在下面那道 mounted 闸【外面】、且无条件执行：
  // 那道闸是给 loadData 防重复请求用的（onMounted 刚拉过一次），
  // 而 MainLayout 的 keep-alive 是 :max="14"，第二次以后进本页只触发 onActivated、
  // 不再触发 onMounted，写进 if 里就只有首次访问才会清零。
  badgesStore.ackDepsFailed();
  if (!mounted) {
    // 带定位 query 进来时由 handleLocateQuery 自己拉数据，这里不再重复拉
    void handleLocateQuery().then((handled) => {
      if (!handled) void loadData();
    });
    // 离开期间 Playwright 可能已经装完，但按钮旁的状态不在这里拉：isPageActive 的监听（Playwright 段 ③）
    // 对「从别的菜单页切回来」同样生效，跟踪还在时则由恢复那一轮 tick 拉，这里再拉就重复发了
  }
  mounted = false;
});

onBeforeUnmount(() => {
  closeSSE();
  stopRefreshTimer();
  // 连同标记一起清：还在路上的那一轮回来后看到标记已灭，就不会在页面卸载后再弹提示
  stopPlaywrightWatch();
  if (locateHighlightTimer) clearTimeout(locateHighlightTimer);
  if (depsLogFlushRaf) {
    cancelAnimationFrame(depsLogFlushRaf);
    depsLogFlushRaf = 0;
  }
});
</script>

<style scoped lang="scss">
.deps-page {
  padding: 0;
}

// 横向钉成 hidden 只在桌面有意义：桌面端页面根自己就是滚动容器（global.scss 的 .dd-scroll-page { overflow: auto }），
// 这条防止横向溢出时在页面底部冒出横向滚动条（open-api 页根同款写法）。
// 移动端刻意不裁：滚动交给外层 .layout-main，页面根本来就是 overflow: visible —— MainLayout.vue 移动端那条
// `.route-shell > :deep(.dd-scroll-page)`（(0,3,0)）一直压着原来这条不分端的 (0,2,0)，移动端 hidden 从没生效过；
// 收进桌面媒体查询是让源码与实际一致，免得有人以为移动端会裁、又绕开贴边。
// 移动端也别补 overflow-x: clip：clip 同样在页面根的边框内侧裁，贴屏幕边缘的批量栏 / 类型页签伸进
// .layout-main 左右留白的那一截照样被切掉（放宽裁剪线的 overflow-clip-margin 在 Safari 上不可用）。
@media screen and (min-width: 769px) {
  .deps-page {
    overflow-x: hidden;
  }
}

.page-header {
  margin-bottom: 18px;

  h2 {
    margin: 0;
    font-size: 22px;
    font-weight: 700;
    color: var(--el-text-color-primary);
    line-height: 1.3;
  }
  .page-subtitle {
    font-size: 13px;
    color: var(--el-text-color-secondary);
    margin: 6px 0 0;
    line-height: 1.6;
    max-width: 720px;
  }
}

.page-title-with-icon {
  display: inline-flex;
  align-items: center;
  gap: 8px;
}

.page-title-with-icon :deep(.el-icon) {
  color: var(--el-color-primary);
}

// ---------- Toolbar ----------
// 工具条：按 design-system §4.2，左槽 1×1 网格叠放主操作与批量栏，勾选前后高度与宽度恒定不跳动。
.toolbar {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  margin: 14px 0;
  gap: 12px;
  flex-wrap: wrap;
  &__left {
    display: grid;
    grid-template-columns: minmax(0, 1fr);
    align-items: start;
    min-width: 0;
    min-height: 39px;
  }
  &__filters {
    display: flex;
    align-items: center;
    gap: 12px;
    flex-wrap: wrap;
    min-width: 0;
    min-height: 39px;
  }
  &__right {
    display: flex;
    align-items: center;
    gap: 10px;
    flex-wrap: wrap;
    min-height: 39px;

    // 清零 EP 默认相邻按钮 12px 左外边距，间距统一由 gap 决定
    :deep(.el-button + .el-button) {
      margin-left: 0;
    }
  }
  &__search {
    width: 240px;
  }
  &__filter {
    width: 140px;
  }
  &__python-version {
    width: 150px;
  }
}

.batch-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  min-height: 39px;

  :deep(.el-button + .el-button) {
    margin-left: 0;
  }
}

.batch-actions__count {
  font-size: 13px;
  color: var(--el-text-color-secondary);
  white-space: nowrap;
  cursor: default;
}

.toolbar__filters,
.batch-actions {
  grid-area: 1 / 1;
  min-width: 0;
  transition: opacity var(--dd-motion-fast) var(--dd-ease-standard);
}

.is-swapped-out {
  opacity: 0;
  visibility: hidden;
}

.python-runtime-option {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
}

.python-runtime-hint {
  margin-bottom: 14px;
}

.python-runtime-hint__body {
  font-size: 13px;
  line-height: 1.6;
}

.python-runtime-hint__status {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: 8px;
}

// Linux 页签说明块。位置与观感完全对齐上面的 Python 说明块，
// 只多一行「安装编译工具链」的操作区。
.linux-runtime-hint {
  margin-bottom: 14px;
}

.linux-runtime-hint__body {
  font-size: 13px;
  line-height: 1.6;
}

.linux-runtime-hint__actions {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 10px;
  margin-top: 8px;
}

.linux-runtime-hint__tip {
  font-size: 12px;
  color: var(--el-text-color-secondary);
}

// Playwright 三项都装齐时的「已就绪」前缀
.linux-runtime-hint__ready {
  color: var(--dd-ok-ink);
  font-weight: 600;
}

// ---------- Table Card ----------
// 表格卡：无阴影，仅用 1px 边框与页面底色区分；本页是滚动页（dd-scroll-page），不做 fixed 高度链处理。
.table-card {
  background: var(--el-bg-color);
  // 表格容器属容器类表面 → surface 档；overflow:hidden 让内部贴边的表头/行自动被圆角裁角
  border-radius: var(--dd-radius-surface);
  border: 1px solid var(--el-border-color-lighter);
  overflow: hidden;
}

// 按 query 定位到的那一行 / 那张卡片：主色浅底约 2 秒，类名撤掉后随过渡淡出。
// 用 inset 阴影叠色而不是直接换 background：--dd-accent-soft 是半透明的，换掉不透明底色后
// 右侧固定的「操作」列横滚时会透出底下的内容，卡片也会透出页面背景。
:deep(.el-table__row.is-locate-highlight > td.el-table__cell),
.dd-mobile-card.is-locate-highlight {
  box-shadow: inset 0 0 0 100vmax var(--dd-accent-soft);
}

:deep(.el-table__row > td.el-table__cell),
.dd-mobile-card {
  // 带上 EP 自己的 background-color 过渡，免得把行悬停的过渡覆盖掉
  transition:
    background-color 0.25s ease,
    box-shadow 0.6s var(--dd-ease-standard);
}

.dep-name-cell {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

// 依赖名首字头像：形状承载语义（圆形=头像/身份标识），两种圆角模式下都固定正圆，不吃 --dd-radius-* 令牌
.dep-name-avatar {
  width: 24px;
  height: 24px;
  border-radius: 50%;
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  font-size: 11px;
  font-weight: 700;
  // --dd-avatar 由模板按首字母内联传入（某个主题状态色令牌）；浅色底 + 墨色字，字色与 --dd-*-ink 同一公式，
  // 原来的实色底白字在浅黄、青色上对比度不到 2:1
  background: color-mix(in srgb, var(--dd-avatar) 14%, transparent);
  color: color-mix(in srgb, var(--dd-avatar) 55%, var(--dd-text));
}

.deps-tabs {
  margin-bottom: 14px;
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}

// 状态分段控件：与定时任务页/执行日志页/订阅页/环境变量页一致的分段容器 + 选中态白底品牌色 + 1px 边框。
// 本页有两组：①运行时切换（Node/Python3/Linux）②状态筛选（.status-tabs--filter，含已安装/失败计数），
// 共用同一套观感，使两组视觉统一。
.status-tabs {
  display: inline-flex;
  background: var(--el-fill-color-light);
  // 分段控件的灰底槽属控件类表面 → control 档（与槽内的项同档，两者一致才不会露出内外错位的角）
  border-radius: var(--dd-radius-control);
  padding: 3px;
  gap: 2px;
}
.status-tab {
  padding: 6px 14px;
  // 分段项属控件类表面 → control 档
  border-radius: var(--dd-radius-control);
  // 未选中态用透明边框占位，选中态只换边框颜色，避免尺寸跳动
  border: 1px solid transparent;
  background: transparent;
  color: var(--el-text-color-secondary);
  font-size: 13px;
  font-weight: 500;
  cursor: pointer;
  transition:
    color var(--dd-motion-fast) var(--dd-ease-standard),
    background-color var(--dd-motion-fast) var(--dd-ease-standard),
    border-color var(--dd-motion-fast) var(--dd-ease-standard);
  white-space: nowrap;
  display: inline-flex;
  align-items: center;
  gap: 5px;
  &:hover {
    color: var(--el-text-color-primary);
  }
  &.active {
    background: var(--el-bg-color);
    color: var(--dd-accent-ink);
    border-color: var(--el-border-color-lighter);
    font-weight: 600;
  }
  // 成功/失败筛选选中态用语义色，标出「这一档筛的是哪类结果」。
  // 语义色只上在标签文字上，角标本身保持中性——见下方说明。
  &--success.active {
    color: var(--dd-ok-ink);
  }
  &--danger.active {
    color: var(--dd-err-ink);
  }
}
// 计数徽标改用 DdBadge（level="info" + show-zero），原来手写的 .status-tab__count 删除。
// 两点变化，都是有意的：
//  1. 统一到全站唯一的角标实现，顺带白拿翻牌动效——本页角标会随 3s 轮询跳字
//     （安装完成时「已安装」+1、「失败」可能同时 +1），跳字比静默换数字更能被注意到。
//  2. 放弃原来「选中态反白成 success/danger 实心」的处理。设计系统里 danger 实心是
//     留给「需要用户处理」的，这三个角标只是中性计数；一个实心红的「失败 0」会和
//     真正要处理的告警抢注意力。现在筛选态由标签文字的语义色表达，角标始终中性描边。
.dep-name-text {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-weight: 500;
  color: var(--el-text-color-primary);
}
// .version-text 随「版本」列一起删除（模型里没有 version 字段，见模板处注释）
.time-text {
  font-family: var(--dd-font-mono);
  font-size: 12px;
  color: var(--el-text-color-regular);
}
// 操作列按钮组。
// 尺寸口径变更：原来这里把按钮压到 height:26px / padding:0 5px / font-size:12px，
// 比全站其它页（定时任务页/执行日志页/订阅页的 padding:4px 8px）更紧一档——那是为了在
// 176px 列宽里硬塞下「详情 + 取消/重装 + 更多▾」三个按钮。现在整列只剩一个 Split Button，
// 不需要再压，统一回其它页的 padding: 4px 8px；列宽估算（详情 40px + caret 24px）也按这个口径算。
// .action-more-btn（手写 caret 的 gap）和 .danger-dropdown-text（手写红色强制卸载）
// 随手工版一起删除：前者由 DdSplitButton 自带的 caret 半边取代，后者由 item.danger 取代。
.action-btns {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 4px;
  min-width: 0;

  // caret 半边保持 EP 自己的窄内边距（--small 档约 24px 宽），
  // 一起套 8px 会把它撑宽，列宽余量随之失准。
  :deep(.el-button:not(.el-dropdown__caret-button)) {
    padding: 4px 8px;
  }

  // EP 自带 `.el-button + .el-button { margin-left: 12px }`，split button 的两个半边
  // 正是相邻的 el-button，不清零会在按钮组中间裂开一道 12px。
  :deep(.el-button + .el-button) {
    margin-left: 0;
  }
}

// ---------- Pagination ----------
// 分页条：与定时任务页/执行日志页/订阅页/环境变量页一致的间距收敛（margin-top 14px）
.pagination-bar {
  margin-top: 14px;
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  padding: 0 4px;
}
.pagination-total {
  font-size: 13px;
  color: var(--el-text-color-secondary);
}

// 表线与常规表格行高交由全局 global.scss + _switches.scss 的密度变量统一接管，
// 已移除局部 .el-table__cell { padding: 8px 0 } 与 EP1 废弃选择器 .el-table__fixed-right。

.auto-split-row {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px 8px;
}

.auto-split-row__hint {
  font-size: 12px;
  line-height: 1.5;
  color: var(--el-text-color-secondary);
}

// ---------- Mobile toolbar（v3.3.1，issue #143）----------
// 只在 isMobile 分支里渲染。第一行 / 批量栏的几何全部来自 global.scss 的共享类
// （.dd-mobile-toolbar / .dd-mobile-batch-bar / .dd-scroll-row / .dd-icon-only-btn），这里只补本页特有的两行。
.deps-mobile-toolbar {
  margin-bottom: 12px;
}

// 类型页签：灰底槽跟随页面 12px 留白（模板挂 .dd-mobile-bleed，与订阅 / 日志的状态分段、任务的视图分组栏一致），
// 三个页签在槽内三等分，保留失败角标。宽度与圆角由 .dd-mobile-bleed 用 !important 接管：
// v3.3.2 / issue #144 起它不再贴边、也不再固定直角，改成吃 --dd-radius-button（rounded 16px / square 0），
// 槽内项在下面的移动端媒体查询里跟到 calc(-3px) 与槽同心。
// 同挂 .dd-scroll-row 只是 320px 这类窄屏的兜底：带两位数角标时放不下就横滑。
// 灰底槽、上下内边距、gap 沿用 .status-tabs；它的 inline-flex（scoped，压过全局 .dd-scroll-row 的 flex）
// 会按内容收宽，本条改回占满整行的 flex。
.status-tabs.deps-type-tabs {
  display: flex;
  width: 100%;
}

.deps-type-tabs .status-tab {
  flex: 1 1 0;
  justify-content: center;
  padding: 6px 8px;
}

// Python 页签的第三行：版本选择占满剩余宽度，「设为默认」取自然宽度
.deps-mobile-python {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: 10px;

  // el-select 是多根组件，根上落不到本组件的 scopeId，只能从自己的容器用 :deep 命中（design-system §5）
  :deep(.el-select) {
    flex: 1 1 0;
    min-width: 0;
  }

  .el-button {
    flex-shrink: 0;
  }
}

// ---------- Log dialog ----------
.log-content {
  // 安装日志面板属容器类表面 → surface 档（弹窗 body 有内边距，不贴边，不会露角）
  border-radius: var(--dd-radius-surface);
  padding: 16px;
  font-family: var(--dd-font-mono);
  font-size: 13px;
  line-height: 1.6;
  min-height: 200px;
  max-height: 60vh;
  overflow-y: auto;
  margin: 0;
  white-space: pre-wrap;
  word-break: break-all;
}

// 移动端全屏安装日志弹窗：解除 60vh 上限，改为 flex 撑满整个视口剩余高度
:deep(.dep-log-dialog.is-fullscreen) {
  .el-dialog__body {
    display: flex;
    flex-direction: column;
    overflow: hidden;
  }

  .log-dialog-toolbar {
    flex-shrink: 0;
  }

  .log-content {
    flex: 1 1 0;
    min-height: 0;
    max-height: none;
  }
}

.log-dialog-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 8px;
}

.log-dialog-status {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  min-width: 0;
}

.mirror-hint {
  font-size: 12px;
  color: var(--el-text-color-secondary);
  line-height: 1.5;
  margin-top: 6px;
}

.running-tag {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

// ===== 状态标签切换过渡 =====
// 表格状态列 / 移动卡片 / 日志弹窗共用。
// 只过渡 opacity：状态标签在表格行里，做位移会连带整行一起晃；
// 做缩放又违反「hover/active 禁 transform 位移或缩放」的同一条扁平约束。
// 时长/缓动全走令牌，prefers-reduced-motion 下令牌自动降到 1ms，等效关闭。
.dd-status-switch-enter-active,
.dd-status-switch-leave-active {
  transition: opacity var(--dd-motion-fast) var(--dd-ease-standard);
}

.dd-status-switch-enter-from,
.dd-status-switch-leave-to {
  opacity: 0;
}

// ---------- Responsive ----------

@media screen and (max-height: 720px) and (min-width: 769px) {
  .android-runtime-card {
    margin-bottom: 12px;

    :deep(.el-card__header) {
      padding: 12px 16px;
    }

    :deep(.el-card__body) {
      padding: 12px 16px;
    }
  }

  .android-runtime-tip {
    margin-bottom: 8px;
  }

  .android-runtime-grid {
    margin-top: 6px;
  }

  .runtime-item {
    padding: 10px 12px;
    margin-bottom: 8px;
  }

  .android-runtime-log pre {
    max-height: 140px;
  }

  .deps-tabs,
  .toolbar {
    margin-bottom: 10px;
  }

  .pagination-bar {
    margin-top: 12px;
  }
}

@media (max-width: 768px) {
  .page-header {
    margin-bottom: 14px;
    h2 {
      font-size: 18px;
    }
  }
  // 第一行离顶栏的 12px 统一由移动端 .layout-main 的上内边距给（MainLayout.vue）。面具版排在最上面的
  // Android 运行时卡也一样，不要再给它补上外边距，否则会与那 12px 叠成 24px。
  // （.toolbar 的移动端竖排规则已删：移动端改走 .deps-mobile-toolbar，.toolbar 只在桌面渲染。）

  .pagination-bar {
    flex-direction: column;
    gap: 10px;
    align-items: center;
  }

  // 槽内项跟着槽一起圆（v3.3.2 / issue #144）：槽吃 --dd-radius-button（16px），
  // 项减 3px 与槽同心，四周灰边才是均匀的 3px；不减的话项的直角会把槽的圆角顶掉、
  // 四角灰边几乎归零，看着像被戳破。square 模式下 calc(0px - 3px) 会被 CSS 夹到 0
  // （global.scss:219 的 .el-button--small 已有同款写法并写明实测结论）。
  // 选择器带 .dd-mobile-bleed 限定：本页 265 / 284 行还有两组桌面才渲染的 .status-tabs，
  // 它们共用 2823 的基类，不能被一起圆掉。
  .status-tabs.dd-mobile-bleed .status-tab {
    border-radius: calc(var(--dd-radius-button) - 3px);
  }
}

// ---------- Android Runtime ----------
.android-runtime-card {
  margin-bottom: 16px;
  border: 1px solid var(--el-border-color-lighter);
}
.android-runtime-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}
.android-runtime-header .el-icon {
  vertical-align: middle;
  margin-right: 6px;
}
.android-runtime-meta {
  color: var(--el-text-color-secondary);
  font-size: 12px;
  display: flex;
  gap: 8px;
  align-items: center;
}
.android-runtime-tip {
  margin-bottom: 12px;
}
.android-runtime-grid {
  margin-top: 8px;
}
.runtime-item {
  border: 1px solid var(--el-border-color-lighter);
  // 运行时条目是弹窗内的列表卡片，属容器类表面 → surface 档
  border-radius: var(--dd-radius-surface);
  padding: 12px 14px;
  margin-bottom: 12px;
  background: var(--el-fill-color-lighter);
}
.runtime-item__head {
  display: flex;
  gap: 8px;
  align-items: center;
  margin-bottom: 6px;
}
.runtime-item__meta {
  font-size: 12px;
  color: var(--el-text-color-secondary);
  line-height: 1.6;
}
.runtime-item__note {
  color: var(--dd-warn-ink);
  margin-top: 4px;
}
.runtime-item__actions {
  margin-top: 10px;
  display: flex;
  gap: 8px;
}
.android-runtime-log {
  margin-top: 12px;
  border-top: 1px dashed var(--el-border-color-lighter);
  padding-top: 10px;
}
.android-runtime-log__title {
  display: flex;
  justify-content: space-between;
  align-items: center;
  font-size: 13px;
  color: var(--el-text-color-secondary);
  margin-bottom: 6px;
}
.android-runtime-log pre {
  background: var(--el-fill-color);
  // 运行时安装日志代码块属容器类表面 → surface 档（上方有虚线分隔+padding-top，不贴边）
  border-radius: var(--dd-radius-surface);
  padding: 10px 12px;
  font-size: 12px;
  max-height: 240px;
  overflow: auto;
  margin: 0;
}

// ===== 入场动画 =====
// v4.0.1 起删除本页手写的 dd-deps-rise-in 动画覆写，交由模板上的全局 .dd-rise（styles/motion.scss）
// 统一管理入场动效与 subtle / off 动效分档。
</style>
