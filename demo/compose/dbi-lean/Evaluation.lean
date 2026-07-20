import Lean.Data.Json
import Lean.Data.Json.FromToJson

open Lean System

structure Params where
  correct_response_feedback : Option String := none
  incorrect_response_feedback : Option String := none
  deriving BEq, FromJson, ToJson

structure ShimmyParams where
  response : String
  answer : String
  params : Option Params
  deriving FromJson

structure InputData where
  command : String
  params : ShimmyParams
  deriving FromJson

structure Result where
  is_correct : Bool
  feedback : String
  deriving BEq, ToJson

structure OutputData where
  command : String
  result : Result
  deriving BEq, ToJson

def compareValues (response : String) (answer : String) : Bool :=
  response == answer

def processInputData (inputData : InputData) : OutputData :=
  let options := inputData.params.params.getD {}
  let isCorrect := compareValues inputData.params.response inputData.params.answer
  let feedback := if isCorrect then
    options.correct_response_feedback.getD "Correct"
  else
    options.incorrect_response_feedback.getD "Incorrect"
  { command := inputData.command, result := { is_correct := isCorrect, feedback := feedback } }

def readInputData (inputFile : String) : IO (Except String InputData) := do
  let inputJson ← IO.FS.readFile inputFile
  match Json.parse inputJson with
  | Except.ok json =>
    match fromJson? json with
    | Except.ok inputData => pure (Except.ok inputData)
    | Except.error err => pure (Except.error s!"Failed to parse input data: {err}")
  | Except.error err => pure (Except.error s!"Failed to parse input JSON: {err}")

def writeOutputData (outputFile : String) (outputData : OutputData) : IO Unit := do
  IO.FS.writeFile outputFile (toJson outputData).compress

def handle (inputFile : String) (outputFile : String) : IO (Except String OutputData) := do
  match ← readInputData inputFile with
  | Except.ok inputData =>
    if inputData.command == "eval" then
      let outputData := processInputData inputData
      writeOutputData outputFile outputData
      pure (Except.ok outputData)
    else
      pure (Except.error "Command not supported")
  | Except.error err => pure (Except.error err)
