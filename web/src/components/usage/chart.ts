import { use } from 'echarts/core'
import { CanvasRenderer } from 'echarts/renderers'
import { BarChart } from 'echarts/charts'
import { AriaComponent, GridComponent, LegendComponent, TooltipComponent } from 'echarts/components'
import VChart from 'vue-echarts'

use([CanvasRenderer, BarChart, AriaComponent, GridComponent, LegendComponent, TooltipComponent])

// VChart 是注册了用量页所需图表与组件的 ECharts 组件，随首张图表单独载入
export default VChart
